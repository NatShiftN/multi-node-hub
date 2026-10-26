package controlplane

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAPIKeyAndV1Allocation(t *testing.T) {
	server, _ := newDeployTestServer(t)
	if err := server.store.CreateProvisioningNode("node-v1", "v1.example.com", "v1.example.com"); err != nil {
		t.Fatalf("create node: %v", err)
	}

	recorder := apiCall(t, server, http.MethodGet, "/v1/openapi.json", "")
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "/v1/traffic/allocate") {
		t.Fatalf("openapi status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = apiCallNoAuth(t, server, http.MethodGet, "/v1/nodes", "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /v1/nodes status = %d", recorder.Code)
	}

	recorder = apiCall(t, server, http.MethodPost, "/api/api-keys", `{"name":"Xboard production"}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create key status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var created struct {
		Token string `json:"token"`
		Key   struct {
			ID     uint   `json:"id"`
			Prefix string `json:"prefix"`
		} `json:"api_key"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode key: %v", err)
	}
	if !strings.HasPrefix(created.Token, "sk-") || created.Key.ID == 0 {
		t.Fatalf("created key = %+v", created)
	}
	if strings.Contains(recorder.Body.String(), "key_hash") {
		t.Fatal("API key hash was exposed")
	}

	recorder = apiCallNoAuthWithToken(t, server, http.MethodGet, "/v1/nodes", "", created.Token)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "node-v1") {
		t.Fatalf("v1 nodes status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	body := `{"email":"merchant@example.com","quota_bytes":10737418240,"device_limit":3,"node_ids":["node-v1"],"reset_usage":true}`
	recorder = apiCallNoAuthWithToken(t, server, http.MethodPost, "/v1/traffic/allocate", body, created.Token)
	if recorder.Code != http.StatusOK {
		t.Fatalf("allocate status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var allocated struct {
		User struct {
			Email       string `json:"email"`
			QuotaBytes  uint64 `json:"quota_bytes"`
			DeviceLimit int    `json:"device_limit"`
		} `json:"user"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &allocated); err != nil {
		t.Fatalf("decode allocation: %v", err)
	}
	if allocated.User.Email != "merchant@example.com" || allocated.User.QuotaBytes != 10737418240 || allocated.User.DeviceLimit != 3 {
		t.Fatalf("allocation = %+v", allocated)
	}
	quotas, err := server.store.ListTrafficQuotas()
	if err != nil || len(quotas) != 1 || quotas[0].DeviceLimit != 3 {
		t.Fatalf("stored quotas = %+v, err=%v", quotas, err)
	}

	recorder = apiCall(t, server, http.MethodDelete, "/api/api-keys/"+jsonNumber(created.Key.ID), "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("revoke status = %d", recorder.Code)
	}
	recorder = apiCallNoAuthWithToken(t, server, http.MethodGet, "/v1/nodes", "", created.Token)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key status = %d", recorder.Code)
	}
}
