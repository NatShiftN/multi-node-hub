package controlplane

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"nodehub/internal/gfwprobe"
	"nodehub/internal/protocol"
)

type blockedProbe struct{}

func (blockedProbe) Name() string { return "blocked-test" }
func (blockedProbe) Check(context.Context, string, int) (gfwprobe.Result, error) {
	return gfwprobe.Result{Blocked: true, Reachable: false, Detail: "tcp reset", Provider: "blocked-test"}, nil
}

func TestProbeLocalSuccessPlusDomesticBlockMarksGFW(t *testing.T) {
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
		t.Fatalf("create node: %v", err)
	}
	if err := server.store.MarkNodeOnline(protocol.AgentInfo{ID: "node-gfw", Hostname: "gfw", Version: "0.9.8"}, "198.51.100.10", time.Now().UTC()); err != nil {
		t.Fatalf("mark online: %v", err)
	}
	server.probeOnce(context.Background())
	node, _ := server.store.GetNode("node-gfw")
	if node.Status != "online" {
		t.Fatalf("first probe status = %q, want online", node.Status)
	}
	server.probeOnce(context.Background())
	node, _ = server.store.GetNode("node-gfw")
	if node.Status != "gfw_blocked" || node.GFWBlockedAt == nil {
		t.Fatalf("second probe status = %q, node = %+v", node.Status, node)
	}
}
