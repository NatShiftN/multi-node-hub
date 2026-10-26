package controlplane

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"nodehub/internal/protocol"
	"nodehub/internal/store"
)

func apiCall(t *testing.T, server *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testAuthToken(t, server))
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	return recorder
}

func testAuthToken(t *testing.T, server *Server) string {
	t.Helper()
	token, _, err := server.auth.issueToken(store.AdminUser{ID: 1, Username: "admin"})
	if err != nil {
		t.Fatalf("issue test JWT: %v", err)
	}
	return token
}

func TestPortForwardCRUDForOfflineNode(t *testing.T) {
	server, _ := newDeployTestServer(t)
	if err := server.store.CreateProvisioningNode("node-offline", "offline.example.com", "offline.example.com"); err != nil {
		t.Fatalf("create node: %v", err)
	}

	recorder := apiCall(t, server, http.MethodPost, "/api/nodes/node-offline/port-forwards",
		`{"listen_port":22022,"protocol":"udp","target_host":"10.0.0.8","target_port":22,"enabled":false}`)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("create status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var created struct {
		Rule   portForwardRuleResponse `json:"rule"`
		Synced bool                    `json:"synced"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Synced || created.Rule.RuleID == "" || created.Rule.ListenPort != 22022 || created.Rule.Protocol != "udp" || created.Rule.Enabled {
		t.Fatalf("unexpected create response: %+v", created)
	}

	recorder = apiCall(t, server, http.MethodGet, "/api/nodes/node-offline/port-forwards", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d", recorder.Code)
	}
	var listed struct {
		Rules []portForwardRuleResponse `json:"rules"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Rules) != 1 || listed.Rules[0].RuleID != created.Rule.RuleID {
		t.Fatalf("unexpected list: %+v", listed)
	}

	path := "/api/nodes/node-offline/port-forwards/" + created.Rule.RuleID
	recorder = apiCall(t, server, http.MethodPatch, path, `{"enabled":true}`)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("patch status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var patched struct {
		Rule portForwardRuleResponse `json:"rule"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if !patched.Rule.Enabled {
		t.Fatal("rule should be enabled after patch")
	}

	recorder = apiCall(t, server, http.MethodPost, "/api/nodes/node-offline/port-forwards",
		`{"listen_port":22022,"target_host":"10.0.0.9","target_port":22}`)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate listen port status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = apiCall(t, server, http.MethodDelete, path, "")
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("delete status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = apiCall(t, server, http.MethodGet, "/api/nodes/node-offline/port-forwards", "")
	var empty struct {
		Rules []portForwardRuleResponse `json:"rules"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &empty)
	if len(empty.Rules) != 0 {
		t.Fatalf("rules after delete: %+v", empty)
	}
}

func TestPortForwardValidationAndNodeNotFound(t *testing.T) {
	server, _ := newDeployTestServer(t)
	if err := server.store.CreateProvisioningNode("node-a", "a.example.com", "a.example.com"); err != nil {
		t.Fatalf("create node: %v", err)
	}

	cases := []struct {
		name string
		path string
		body string
		want int
	}{
		{
			name: "invalid listen port",
			path: "/api/nodes/node-a/port-forwards",
			body: `{"listen_port":0,"target_host":"10.0.0.8","target_port":22}`,
			want: http.StatusBadRequest,
		},
		{
			name: "invalid target host",
			path: "/api/nodes/node-a/port-forwards",
			body: `{"listen_port":22022,"target_host":"http://bad","target_port":22}`,
			want: http.StatusBadRequest,
		},
		{
			name: "unknown node",
			path: "/api/nodes/missing/port-forwards",
			body: `{"listen_port":22022,"target_host":"10.0.0.8","target_port":22}`,
			want: http.StatusNotFound,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			recorder := apiCall(t, server, http.MethodPost, test.path, test.body)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d, body = %s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}

func TestPortForwardSyncToOnlineAgent(t *testing.T) {
	server, _ := newDeployTestServer(t)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	conn := dialTestAgent(t, httpServer.URL, "shared-token", "node-online")
	defer conn.Close()
	writeHello(t, conn, "node-online")

	// Emulate an Agent: acknowledge every port_forward command.
	commands := make(chan protocol.Envelope, 8)
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var env protocol.Envelope
			if err := json.Unmarshal(raw, &env); err != nil {
				continue
			}
			if env.Type != protocol.TypeCommand {
				continue
			}
			commands <- env
			response := protocol.Result(env.RequestID, env.Plugin, protocol.PortForwardResult{ActiveRules: 1}, nil)
			if rawResponse, err := json.Marshal(response); err == nil {
				if err := conn.WriteMessage(websocket.TextMessage, rawResponse); err != nil {
					return
				}
			}
		}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := server.hub.Get("node-online"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("agent did not register")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Drain the initial desired-state syncs triggered by registration. The
	// server also syncs an empty Xray desired state on connect.
	deadline = time.Now().Add(2 * time.Second)
	gotInitialPortForward := false
	for !gotInitialPortForward {
		select {
		case cmd := <-commands:
			if cmd.Plugin == "port_forward" {
				gotInitialPortForward = true
			}
		case <-time.After(time.Until(deadline)):
			t.Fatal("agent did not receive initial port_forward sync")
		}
	}

	recorder := apiCall(t, server, http.MethodPost, "/api/nodes/node-online/port-forwards",
		`{"listen_port":22023,"target_host":"10.0.0.8","target_port":22}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Synced bool `json:"synced"`
		Rule   struct {
			RuleID string `json:"rule_id"`
		} `json:"rule"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Synced || response.Rule.RuleID == "" {
		t.Fatalf("unexpected response: %+v", response)
	}

	deadline = time.Now().Add(2 * time.Second)
	for {
		select {
		case cmd := <-commands:
			if cmd.Plugin != "port_forward" {
				continue
			}
			var payload protocol.PortForwardPayload
			if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
				t.Fatalf("decode port_forward payload: %v", err)
			}
			if len(payload.Rules) != 1 || payload.Rules[0].ListenPort != 22023 {
				t.Fatalf("unexpected rules payload: %+v", payload)
			}
			return
		case <-time.After(time.Until(deadline)):
			t.Fatal("agent did not receive the updated port_forward command")
		}
	}
}
