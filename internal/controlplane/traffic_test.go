package controlplane

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"nodehub/internal/protocol"
)

func TestListUserTrafficAPI(t *testing.T) {
	server, _ := newDeployTestServer(t)
	if err := server.store.CreateProvisioningNode("node-a", "a.example.com", "a.example.com"); err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := server.store.ApplyTrafficUpdate("node-a", protocol.TrafficUpdate{
		SessionID: "s1",
		Timestamp: time.Now().UTC(),
		Users: []protocol.TrafficUserStat{
			{Email: "alice@example.com", UploadBytes: 100, DownloadBytes: 200},
		},
	}, time.Now().UTC()); err != nil {
		t.Fatalf("ApplyTrafficUpdate: %v", err)
	}

	recorder := apiCall(t, server, http.MethodGet, "/api/nodes/node-a/traffic", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Users []userTrafficResponse `json:"users"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(response.Users) != 1 || response.Users[0].UserEmail != "alice@example.com" {
		t.Fatalf("unexpected users: %+v", response.Users)
	}
	if response.Users[0].TotalBytes != 300 {
		t.Fatalf("total = %d", response.Users[0].TotalBytes)
	}

	recorder = apiCall(t, server, http.MethodGet, "/api/nodes/missing/traffic", "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing node status = %d", recorder.Code)
	}
}
