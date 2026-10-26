package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"nodehub/internal/protocol"
)

const quotaTestConfig = `{
  "inbounds": [
    {
      "tag": "vless-in",
      "protocol": "vless",
      "settings": {
        "clients": [
          {"id": "11111111-1111-1111-1111-111111111111", "email": "alice@example.com"},
          {"id": "22222222-2222-2222-2222-222222222222", "email": "bob@example.com"}
        ]
      }
    }
  ],
  "outbounds": [{"protocol": "freedom"}]
}`

func TestRemoveXrayIdentity(t *testing.T) {
	updated, changed, err := removeXrayIdentity(quotaTestConfig, "alice@example.com")
	if err != nil || !changed {
		t.Fatalf("removeXrayIdentity changed=%v err=%v", changed, err)
	}
	if strings.Contains(updated, "alice@example.com") {
		t.Fatalf("alice remains in config: %s", updated)
	}
	if !strings.Contains(updated, "bob@example.com") {
		t.Fatalf("bob was removed: %s", updated)
	}

	byID, changed, err := removeXrayIdentity(quotaTestConfig, "22222222-2222-2222-2222-222222222222")
	if err != nil || !changed || strings.Contains(byID, "bob@example.com") {
		t.Fatalf("remove by UUID changed=%v err=%v config=%s", changed, err, byID)
	}

	if _, changed, err := removeXrayIdentity(quotaTestConfig, "nobody@example.com"); err != nil || changed {
		t.Fatalf("unexpected removal changed=%v err=%v", changed, err)
	}
}

func TestQuotaEnforcementRemovesClientFromStoredConfig(t *testing.T) {
	server, _ := newDeployTestServer(t)
	if err := server.store.CreateProvisioningNode("node-a", "a.example.com", "a.example.com"); err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := server.store.SaveXrayConfig("node-a", quotaTestConfig); err != nil {
		t.Fatalf("save xray config: %v", err)
	}
	now := time.Now().UTC()
	if err := server.store.ApplyTrafficUpdate("node-a", protocol.TrafficUpdate{
		SessionID: "s1",
		Users: []protocol.TrafficUserStat{
			{Email: "alice@example.com", UploadBytes: 200, DownloadBytes: 200},
		},
	}, now); err != nil {
		t.Fatalf("apply traffic: %v", err)
	}
	if err := server.store.SetUserQuota("alice@example.com", 300); err != nil {
		t.Fatalf("set quota: %v", err)
	}
	banned, err := server.store.BanExceededUsers(now)
	if err != nil || len(banned) != 1 {
		t.Fatalf("banned = %+v, err = %v", banned, err)
	}
	server.enforceBannedUser(context.Background(), "node-a", "alice@example.com")
	stored, err := server.store.GetXrayConfig("node-a")
	if err != nil {
		t.Fatalf("get xray config: %v", err)
	}
	if strings.Contains(stored.Config, "alice@example.com") {
		t.Fatalf("alice was not removed: %s", stored.Config)
	}
}

func TestTrafficQuotaAPI(t *testing.T) {
	server, _ := newDeployTestServer(t)
	if err := server.store.CreateProvisioningNode("node-a", "a.example.com", "a.example.com"); err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := server.store.ApplyTrafficUpdate("node-a", protocol.TrafficUpdate{
		SessionID: "s1",
		Users: []protocol.TrafficUserStat{
			{Email: "alice@example.com", UploadBytes: 100, DownloadBytes: 200},
		},
	}, time.Now().UTC()); err != nil {
		t.Fatalf("apply traffic: %v", err)
	}

	recorder := apiCall(t, server, http.MethodPut, "/api/traffic/users/alice%40example.com/quota", `{"quota_bytes":5000}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("set quota status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = apiCall(t, server, http.MethodGet, "/api/nodes/node-a/traffic", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("node traffic status = %d", recorder.Code)
	}
	var response struct {
		Users []userTrafficResponse `json:"users"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(response.Users) != 1 || response.Users[0].QuotaBytes != 5000 || response.Users[0].IsBanned {
		t.Fatalf("unexpected traffic response: %+v", response.Users)
	}

	recorder = apiCall(t, server, http.MethodGet, "/api/traffic/users", "")
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "alice@example.com") {
		t.Fatalf("traffic users status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}
