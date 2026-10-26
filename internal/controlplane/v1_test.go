package controlplane

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"nodehub/internal/protocol"
	"nodehub/internal/store"
)

func TestNodesServerSidePaginationAndFilters(t *testing.T) {
	server, _ := newDeployTestServer(t)
	for i := 0; i < 25; i++ {
		id := "node-" + strconv.Itoa(i)
		if err := server.store.CreateProvisioningNode(id, "Node "+strconv.Itoa(i), "10.0.0."+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			if err := server.store.SetNodeTags(id, []string{"hk"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	recorder := apiCall(t, server, http.MethodGet, "/api/nodes?page=1&limit=2&search=node-2&tag=hk", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Nodes      []NodeResponse `json:"nodes"`
		Total      int64          `json:"total"`
		Page       int            `json:"page"`
		Limit      int            `json:"limit"`
		TotalPages int64          `json:"total_pages"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Page != 1 || response.Limit != 2 || response.Total == 0 || len(response.Nodes) == 0 {
		t.Fatalf("unexpected page response: %+v", response)
	}
	for _, node := range response.Nodes {
		if !strings.Contains(node.ID, "node-2") || len(node.Tags) == 0 {
			t.Fatalf("filter leak: %+v", node)
		}
	}
}

func TestRBACViewerOperatorAndNodeTags(t *testing.T) {
	server, _ := newDeployTestServer(t)
	for _, node := range []struct{ id, tag string }{{"node-hk", "hk"}, {"node-us", "us"}} {
		if err := server.store.CreateProvisioningNode(node.id, node.id, node.id+".example.com"); err != nil {
			t.Fatal(err)
		}
		if err := server.store.SetNodeTags(node.id, []string{node.tag}); err != nil {
			t.Fatal(err)
		}
	}
	createAccount := func(username, password, role, tags string) {
		hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
		if _, err := server.store.CreateAdminUserWithRole(username, string(hash), role, tags); err != nil {
			t.Fatal(err)
		}
	}
	createAccount("viewer1", "viewerpass1", store.RoleViewer, "[]")
	createAccount("operator1", "operatorpass1", store.RoleOperator, `["hk"]`)
	login := func(username, password string) string {
		recorder := apiCallNoAuth(t, server, http.MethodPost, "/api/login", `{"username":"`+username+`","password":"`+password+`"}`)
		if recorder.Code != http.StatusOK {
			t.Fatalf("login %s: %s", username, recorder.Body.String())
		}
		var response struct {
			Token string `json:"token"`
		}
		_ = json.Unmarshal(recorder.Body.Bytes(), &response)
		return response.Token
	}
	viewer := login("viewer1", "viewerpass1")
	operator := login("operator1", "operatorpass1")

	if recorder := apiCallNoAuthWithToken(t, server, http.MethodGet, "/api/nodes", "", viewer); recorder.Code != http.StatusOK {
		t.Fatalf("viewer GET = %d", recorder.Code)
	}
	if recorder := apiCallNoAuthWithToken(t, server, http.MethodPut, "/api/nodes/node-hk/tags", `{"tags":["hk"]}`, viewer); recorder.Code != http.StatusForbidden {
		t.Fatalf("viewer PUT = %d", recorder.Code)
	}
	if recorder := apiCallNoAuthWithToken(t, server, http.MethodPut, "/api/nodes/node-hk/tags", `{"tags":["hk"]}`, operator); recorder.Code != http.StatusOK {
		t.Fatalf("operator allowed tag PUT = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := apiCallNoAuthWithToken(t, server, http.MethodPut, "/api/nodes/node-us/tags", `{"tags":["us"]}`, operator); recorder.Code != http.StatusForbidden {
		t.Fatalf("operator foreign tag PUT = %d", recorder.Code)
	}
	if recorder := apiCallNoAuthWithToken(t, server, http.MethodPost, "/api/nodes/node-hk/dd", `{}`, operator); recorder.Code != http.StatusForbidden {
		t.Fatalf("operator DD = %d", recorder.Code)
	}
	if recorder := apiCallNoAuthWithToken(t, server, http.MethodPost, "/api/nodes/node-hk/command", `{"plugin":"reboot","payload":{}}`, operator); recorder.Code != http.StatusForbidden {
		t.Fatalf("operator reboot command = %d", recorder.Code)
	}
}

func TestDeleteNodeDecommissionsAndBlocksReconnect(t *testing.T) {
	server, _ := newDeployTestServer(t)
	if err := server.store.CreateProvisioningNode("node-delete", "delete", "delete.example.com"); err != nil {
		t.Fatal(err)
	}
	recorder := apiCall(t, server, http.MethodDelete, "/api/nodes/node-delete", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", recorder.Code, recorder.Body.String())
	}
	if _, err := server.store.GetNode("node-delete"); !store.IsNotFound(err) {
		t.Fatalf("node still exists: %v", err)
	}
	blocked, err := server.store.IsNodeDecommissioned("node-delete")
	if err != nil || !blocked {
		t.Fatalf("blocked=%v err=%v", blocked, err)
	}
}

func TestCloudPowerAndBandwidthAPI(t *testing.T) {
	server, _ := newDeployTestServer(t)
	if err := server.store.CreateProvisioningNode("node-cloud", "cloud", "cloud.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := server.store.UpdateNodeCloudSettings("node-cloud", "mock", "instance-cloud", "", "", "", false); err != nil {
		t.Fatal(err)
	}
	recorder := apiCall(t, server, http.MethodPost, "/api/nodes/node-cloud/cloud/power", `{"action":"reboot"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("power = %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = apiCall(t, server, http.MethodGet, "/api/nodes/node-cloud/cloud/bandwidth", "")
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "allocated_bytes") {
		t.Fatalf("bandwidth = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestDashboardStatsAndAdminUsers(t *testing.T) {
	server, _ := newDeployTestServer(t)
	if err := server.store.CreateProvisioningNode("node-stats", "stats", "stats.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := server.store.MarkNodeOnline(protocol.AgentInfo{ID: "node-stats", Hostname: "stats", Version: "1.0.0"}, "198.51.100.3", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := server.store.ApplyTrafficUpdate("node-stats", protocol.TrafficUpdate{
		SessionID: "s1",
		Users:     []protocol.TrafficUserStat{{Email: "u@example.com", UploadBytes: 1000, DownloadBytes: 2000}},
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	recorder := apiCall(t, server, http.MethodGet, "/api/dashboard/stats", "")
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "monthly_upload_bytes") {
		t.Fatalf("stats = %d %s", recorder.Code, recorder.Body.String())
	}
	var stats dashboardStats
	_ = json.Unmarshal(recorder.Body.Bytes(), &stats)
	if stats.NodesOnline != 1 || stats.MonthlyUpload != 1000 || stats.MonthlyDownload != 2000 {
		t.Fatalf("stats = %+v", stats)
	}

	recorder = apiCall(t, server, http.MethodPost, "/api/admin/users",
		`{"username":"subop","password":"suboppass","role":"operator","allowed_tags":["hk"]}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create admin = %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = apiCall(t, server, http.MethodGet, "/api/admin/users", "")
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "password") {
		t.Fatalf("list admins = %d %s", recorder.Code, recorder.Body.String())
	}
}
