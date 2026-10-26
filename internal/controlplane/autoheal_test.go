package controlplane

import (
	"testing"
	"time"
)

func TestAutoHealReplacesIPAndHonorsCooldown(t *testing.T) {
	server, _ := newDeployTestServer(t)
	if err := server.store.CreateProvisioningNode("node-h", "h.example.com", "h.example.com"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := server.store.UpdateNodeIPReplacement("node-h", "192.0.2.20", ""); err != nil {
		t.Fatalf("initial IP: %v", err)
	}
	if err := server.store.UpdateNodeCloudSettings(
		"node-h", "mock", "instance-h", "node-h.example.com", "zone-h", "record-h", true,
	); err != nil {
		t.Fatalf("settings: %v", err)
	}

	server.maybeAutoHeal("node-h")
	deadline := time.Now().Add(3 * time.Second)
	var firstIP string
	for time.Now().Before(deadline) {
		node, err := server.store.GetNode("node-h")
		if err == nil && node.RemoteIP != "192.0.2.20" && node.RemoteIP != "" {
			firstIP = node.RemoteIP
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if firstIP == "" {
		t.Fatal("auto-heal did not replace the IP")
	}
	node, err := server.store.GetNode("node-h")
	if err != nil || node.LastAutoHealAt == nil {
		t.Fatalf("node after heal = %+v, err=%v", node, err)
	}
	firstClaim := *node.LastAutoHealAt

	server.maybeAutoHeal("node-h")
	time.Sleep(300 * time.Millisecond)
	node, _ = server.store.GetNode("node-h")
	if node.LastAutoHealAt == nil || !node.LastAutoHealAt.Equal(firstClaim) {
		t.Fatalf("cooldown was bypassed: first=%v now=%v", firstClaim, node.LastAutoHealAt)
	}

	entries, total, err := server.store.ListAuditLogs(1, 20)
	if err != nil || total == 0 {
		t.Fatalf("auto-heal audit entries=%+v total=%d err=%v", entries, total, err)
	}
	found := false
	for _, entry := range entries {
		if entry.Action == "auto_heal" && entry.Username == "system:autoheal" && entry.TargetNodeID == "node-h" {
			found = true
		}
	}
	if !found {
		t.Fatalf("auto-heal audit entry missing: %+v", entries)
	}
}
