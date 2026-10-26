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
)

func TestWebSocketAcceptsDeploymentTokenForBoundNode(t *testing.T) {
	server, _ := newDeployTestServer(t)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	const nodeID = "node-dynamic-auth"
	token, err := server.tokens.Issue(nodeID)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	conn := dialTestAgent(t, httpServer.URL, token, nodeID)
	defer conn.Close()
	writeHello(t, conn, nodeID)

	deadline := time.Now().Add(time.Second)
	for {
		if _, ok := server.hub.Get(nodeID); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("agent was not registered in the hub")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWebSocketRejectsWrongTokenOrBoundNode(t *testing.T) {
	server, _ := newDeployTestServer(t)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	token, err := server.tokens.Issue("node-bound")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	tests := []struct {
		name   string
		token  string
		nodeID string
	}{
		{name: "wrong token", token: "not-the-token", nodeID: "node-bound"},
		{name: "wrong node id", token: token, nodeID: "node-other"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			url := wsURL(httpServer.URL)
			headers := http.Header{}
			headers.Set("Authorization", "Bearer "+test.token)
			headers.Set("X-Agent-ID", test.nodeID)
			conn, response, err := websocket.DefaultDialer.Dial(url, headers)
			if conn != nil {
				_ = conn.Close()
			}
			if response != nil {
				_ = response.Body.Close()
			}
			if err == nil {
				t.Fatal("expected authentication failure")
			}
			if response == nil || response.StatusCode != http.StatusUnauthorized {
				t.Fatalf("response = %+v, want 401", response)
			}
		})
	}
}

func dialTestAgent(t *testing.T, httpURL, token, nodeID string) *websocket.Conn {
	t.Helper()
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("X-Agent-ID", nodeID)
	conn, response, err := websocket.DefaultDialer.Dial(wsURL(httpURL), headers)
	if response != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial agent websocket: %v", err)
	}
	return conn
}

func writeHello(t *testing.T, conn *websocket.Conn, nodeID string) {
	t.Helper()
	hello := protocol.Envelope{
		Type: protocol.TypeHello,
		Agent: &protocol.AgentInfo{
			ID:        nodeID,
			Hostname:  "test-host",
			OS:        "linux",
			Arch:      "amd64",
			Version:   "test",
			Plugins:   []string{"system_info"},
			StartedAt: time.Now().UTC(),
		},
		SentAt: time.Now().UTC(),
	}
	raw, err := json.Marshal(hello)
	if err != nil {
		t.Fatalf("marshal hello: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatalf("write hello: %v", err)
	}
}

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http") + "/ws/agent"
}
