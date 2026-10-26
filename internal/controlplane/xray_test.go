package controlplane

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestXrayConfigCRUDForOfflineNode(t *testing.T) {
	server, _ := newDeployTestServer(t)
	if err := server.store.CreateProvisioningNode("node-offline", "offline.example.com", "offline.example.com"); err != nil {
		t.Fatalf("create node: %v", err)
	}

	recorder := apiCall(t, server, http.MethodGet, "/api/nodes/node-offline/xray", "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("initial GET status = %d, want 404", recorder.Code)
	}

	recorder = apiCall(t, server, http.MethodPut, "/api/nodes/node-offline/xray",
		`{"config":{"inbounds":[],"outbounds":[{"protocol":"freedom"}]}}`)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("PUT status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var put struct {
		Synced bool `json:"synced"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &put); err != nil {
		t.Fatalf("decode PUT: %v", err)
	}
	if put.Synced {
		t.Fatal("offline node should report synced=false")
	}

	recorder = apiCall(t, server, http.MethodGet, "/api/nodes/node-offline/xray", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var got struct {
		Config json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if len(got.Config) == 0 || got.Config[0] != '{' {
		t.Fatalf("unexpected config: %s", got.Config)
	}

	recorder = apiCall(t, server, http.MethodDelete, "/api/nodes/node-offline/xray", "")
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("DELETE status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = apiCall(t, server, http.MethodGet, "/api/nodes/node-offline/xray", "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("GET after delete status = %d, want 404", recorder.Code)
	}
}

func TestXrayConfigValidation(t *testing.T) {
	server, _ := newDeployTestServer(t)
	if err := server.store.CreateProvisioningNode("node-a", "a.example.com", "a.example.com"); err != nil {
		t.Fatalf("create node: %v", err)
	}
	for _, body := range []string{
		`{"config":null}`,
		`{"config":[]}`,
		`{"config":"not-json"}`,
		`{}`,
	} {
		recorder := apiCall(t, server, http.MethodPut, "/api/nodes/node-a/xray", body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400", body, recorder.Code)
		}
	}

	recorder := apiCall(t, server, http.MethodPut, "/api/nodes/missing/xray",
		`{"config":{"inbounds":[],"outbounds":[]}}`)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown node status = %d, want 404", recorder.Code)
	}
}
