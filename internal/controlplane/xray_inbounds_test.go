package controlplane

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestProxyInboundLifecycleGeneratesXrayConfig(t *testing.T) {
	server, _ := newDeployTestServer(t)
	if err := server.store.CreateProvisioningNode("node-inbound", "edge.example.com", "edge.example.com"); err != nil {
		t.Fatalf("create node: %v", err)
	}

	recorder := apiCall(t, server, http.MethodPost, "/api/nodes/node-inbound/inbounds/reality-keys", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("keys status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var keys struct {
		PrivateKey string `json:"private_key"`
		PublicKey  string `json:"public_key"`
		ShortID    string `json:"short_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &keys); err != nil {
		t.Fatalf("decode keys: %v", err)
	}
	if keys.PrivateKey == "" || keys.PublicKey == "" || keys.ShortID == "" {
		t.Fatalf("keys = %+v", keys)
	}

	body := `{
		"remark":"Reality-443",
		"port":443,
		"protocol":"vless",
		"network":"tcp",
		"security":"reality",
		"settings":{
			"uuid":"11111111-1111-4111-8111-111111111111",
			"private_key":"` + keys.PrivateKey + `",
			"public_key":"` + keys.PublicKey + `",
			"short_id":"` + keys.ShortID + `",
			"dest":"www.microsoft.com:443"
		}
	}`
	recorder = apiCall(t, server, http.MethodPost, "/api/nodes/node-inbound/inbounds", body)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("create status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var created struct {
		Inbound struct {
			ID       uint   `json:"id"`
			Protocol string `json:"protocol"`
			Security string `json:"security"`
		} `json:"inbound"`
		Synced bool `json:"synced"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Inbound.ID == 0 || created.Inbound.Protocol != "vless" || created.Inbound.Security != "reality" {
		t.Fatalf("created = %+v", created)
	}
	if created.Synced {
		t.Fatal("offline node should not be synced")
	}

	records, err := server.store.ListProxyInbounds("node-inbound")
	if err != nil || len(records) != 1 {
		t.Fatalf("records = %+v, err=%v", records, err)
	}
	storedConfig, err := server.store.GetXrayConfig("node-inbound")
	if err != nil {
		t.Fatalf("generated config: %v", err)
	}
	var generated map[string]any
	if err := json.Unmarshal([]byte(storedConfig.Config), &generated); err != nil {
		t.Fatalf("decode generated config: %v", err)
	}
	if _, ok := generated["stats"]; !ok {
		t.Fatal("generated config missing stats")
	}
	inbounds := generated["inbounds"].([]any)
	if len(inbounds) != 2 {
		t.Fatalf("generated inbounds = %d", len(inbounds))
	}

	recorder = apiCall(t, server, http.MethodGet, "/api/nodes/node-inbound/inbounds", "")
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Reality-443") {
		t.Fatalf("list status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	sharePath := "/api/nodes/node-inbound/inbounds/" + jsonNumber(created.Inbound.ID) + "/share"
	recorder = apiCall(t, server, http.MethodGet, sharePath, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("share status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var share struct {
		Link string `json:"link"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &share); err != nil {
		t.Fatalf("decode share: %v", err)
	}
	parsed, err := url.Parse(share.Link)
	if err != nil || parsed.Scheme != "vless" || parsed.Query().Get("security") != "reality" {
		t.Fatalf("share link = %q, err=%v", share.Link, err)
	}

	recorder = apiCall(t, server, http.MethodGet, "/api/nodes/node-inbound/subscription", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("subscription status = %d", recorder.Code)
	}
	decoded, err := base64.StdEncoding.DecodeString(recorder.Body.String())
	if err != nil || !strings.HasPrefix(string(decoded), "vless://") {
		t.Fatalf("subscription = %q, err=%v", recorder.Body.String(), err)
	}

	updatePath := "/api/nodes/node-inbound/inbounds/" + jsonNumber(created.Inbound.ID)
	recorder = apiCall(t, server, http.MethodPatch, updatePath, strings.Replace(body, `"port":443`, `"port":8443`, 1))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("update status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	updated, _ := server.store.ListProxyInbounds("node-inbound")
	if len(updated) != 1 || updated[0].Port != 8443 {
		t.Fatalf("updated = %+v", updated)
	}

	recorder = apiCall(t, server, http.MethodDelete, updatePath, "")
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("delete status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if _, err := server.store.GetXrayConfig("node-inbound"); err == nil {
		t.Fatal("xray config should be removed after last inbound")
	}
	if records, _ := server.store.ListProxyInbounds("node-inbound"); len(records) != 0 {
		t.Fatalf("records after delete = %+v", records)
	}
}

func TestProxyInboundValidationAndPortConflict(t *testing.T) {
	server, _ := newDeployTestServer(t)
	if err := server.store.CreateProvisioningNode("node-inbound", "edge.example.com", "edge.example.com"); err != nil {
		t.Fatalf("create node: %v", err)
	}
	invalid := `{"remark":"bad","port":8388,"protocol":"shadowsocks","network":"ws","security":"none","settings":{"method":"aes-256-gcm","password":"password"}}`
	recorder := apiCall(t, server, http.MethodPost, "/api/nodes/node-inbound/inbounds", invalid)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	first := `{"remark":"one","port":8081,"protocol":"trojan","network":"tcp","security":"none","settings":{"password":"password"}}`
	recorder = apiCall(t, server, http.MethodPost, "/api/nodes/node-inbound/inbounds", first)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	second := `{"remark":"two","port":8081,"protocol":"trojan","network":"tcp","security":"none","settings":{"password":"password"}}`
	recorder = apiCall(t, server, http.MethodPost, "/api/nodes/node-inbound/inbounds", second)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func jsonNumber(value uint) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
