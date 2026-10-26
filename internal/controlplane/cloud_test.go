package controlplane

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestReplaceIPWithMockProviderAndDDNS(t *testing.T) {
	server, _ := newDeployTestServer(t)
	if err := server.store.CreateProvisioningNode("node-cloud", "cloud.example.com", "cloud.example.com"); err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := server.store.UpdateNodeIPReplacement("node-cloud", "192.0.2.10", ""); err != nil {
		t.Fatalf("set initial IP: %v", err)
	}
	if err := server.store.UpdateNodeCloudSettings(
		"node-cloud", "mock", "instance-123", "node-cloud.example.com", "zone-123", "record-123", false,
	); err != nil {
		t.Fatalf("set cloud settings: %v", err)
	}

	recorder := apiCall(t, server, http.MethodPost, "/api/nodes/node-cloud/replace-ip", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("replace status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		NewIP       string `json:"new_ip"`
		OldIP       string `json:"old_ip"`
		DDNSUpdated bool   `json:"ddns_updated"`
		Warning     string `json:"warning"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.NewIP == "" || response.NewIP == response.OldIP {
		t.Fatalf("unexpected IPs: %+v", response)
	}
	if !response.DDNSUpdated || response.Warning != "" {
		t.Fatalf("DDNS result: %+v", response)
	}
	node, err := server.store.GetNode("node-cloud")
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if node.RemoteIP != response.NewIP {
		t.Fatalf("stored IP = %q, want %q", node.RemoteIP, response.NewIP)
	}
}

func TestCloudSettingsAPIValidation(t *testing.T) {
	server, _ := newDeployTestServer(t)
	if err := server.store.CreateProvisioningNode("node-a", "a.example.com", "a.example.com"); err != nil {
		t.Fatalf("create node: %v", err)
	}
	recorder := apiCall(t, server, http.MethodPatch, "/api/nodes/node-a/cloud",
		`{"cloud_provider":"mock","provider_instance_id":"i-1","ddns_domain":"bad domain","ddns_zone_id":"z"}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid domain status = %d", recorder.Code)
	}
	recorder = apiCall(t, server, http.MethodPatch, "/api/nodes/node-a/cloud",
		`{"cloud_provider":"unknown","provider_instance_id":"i-1"}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown provider status = %d", recorder.Code)
	}
	recorder = apiCall(t, server, http.MethodPatch, "/api/nodes/node-a/cloud",
		`{"cloud_provider":"mock","provider_instance_id":"i-1","ddns_domain":"a.example.com","ddns_zone_id":"zone-1"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("valid settings status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	node, _ := server.store.GetNode("node-a")
	if node.CloudProvider != "mock" || node.DDNSDomain != "a.example.com" {
		t.Fatalf("stored node = %+v", node)
	}
}
