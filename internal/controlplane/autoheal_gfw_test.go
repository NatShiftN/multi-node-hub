package controlplane

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"nodehub/internal/protocol"
)

func TestAutoHealDoesNotRunForPlainNetworkFailure(t *testing.T) {
	server, _ := newDeployTestServer(t)
	server.cfg.ProbeFailureThreshold = 1
	server.cfg.ProbePort = freePort(t)
	if err := server.store.CreateProvisioningNode("node-net", "net", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := server.store.MarkNodeOnline(protocol.AgentInfo{ID: "node-net", Hostname: "net", Version: "0.9.8"}, "198.51.100.10", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := server.store.UpdateNodeCloudSettings("node-net", "mock", "instance-net", "", "", "", true); err != nil {
		t.Fatal(err)
	}
	server.probeOnce(context.Background())
	node, _ := server.store.GetNode("node-net")
	if node.Status != "offline_network_error" {
		t.Fatalf("status = %q", node.Status)
	}
	time.Sleep(200 * time.Millisecond)
	node, _ = server.store.GetNode("node-net")
	if node.LastAutoHealAt != nil {
		t.Fatal("plain network failure must not trigger AutoHeal")
	}
}

func TestAutoHealRunsForDomesticBlock(t *testing.T) {
	server, _ := newDeployTestServer(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	server.cfg.ProbePort = port
	server.cfg.GFWProbeThreshold = 2
	server.gfwProbe = blockedProbe{}
	if err := server.store.CreateProvisioningNode("node-gfw", "gfw", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := server.store.MarkNodeOnline(protocol.AgentInfo{ID: "node-gfw", Hostname: "gfw", Version: "0.9.8"}, "198.51.100.10", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := server.store.UpdateNodeCloudSettings("node-gfw", "mock", "instance-gfw", "", "", "", true); err != nil {
		t.Fatal(err)
	}
	server.probeOnce(context.Background())
	server.probeOnce(context.Background())
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		node, _ := server.store.GetNode("node-gfw")
		if node.LastAutoHealAt != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("gfw_blocked did not trigger AutoHeal")
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	_ = listener.Close()
	return port
}
