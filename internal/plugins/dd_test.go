package plugins

import (
	"context"
	"strings"
	"testing"

	"nodehub/internal/protocol"
)

func TestBuildDDPostInstallIncludesAgentRecovery(t *testing.T) {
	script := buildDDPostInstall(protocol.DDPayload{
		NodeID:    "edge-demo-01",
		Token:     "token-abc",
		ServerURL: "wss://hub.example.com/ws/agent",
		AgentURL:  "https://hub.example.com/downloads/nodehub-agent-linux-amd64",
	})
	for _, expected := range []string{
		"curl -fsSL",
		"/usr/local/bin/nodehub-agent",
		"systemctl enable --now nodehub-agent",
		"--server \"wss://hub.example.com/ws/agent\"",
		"--token \"token-abc\"",
		"--id \"edge-demo-01\"",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("missing %q in post-install script:\n%s", expected, script)
		}
	}
}

func TestDDPluginRejectsUnsafeMetadata(t *testing.T) {
	plugin, err := NewDDPlugin(DDPluginConfig{Logger: testPluginLogger()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plugin.Execute(context.Background(), []byte(`{"node_id":"edge-1;id","token":"x","server_url":"ws://hub/ws","agent_url":"http://hub/agent","distro":"debian","version":"12","arch":"amd64"}`)); err == nil {
		t.Fatal("expected invalid node id error")
	}
	if _, err := plugin.Execute(context.Background(), []byte(`{"node_id":"edge-1","token":"x","server_url":"http://hub/ws","agent_url":"http://hub/agent","distro":"debian","version":"12","arch":"amd64"}`)); err == nil {
		t.Fatal("expected invalid server URL error")
	}
}
