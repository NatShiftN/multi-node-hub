package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"

	"nodehub/internal/protocol"
)

type recordedCommand struct {
	name string
	args []string
}

type fakeRunner struct {
	mu     sync.Mutex
	chains map[string]bool
	calls  []recordedCommand
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, recordedCommand{name: name, args: append([]string(nil), args...)})
	if name != "iptables" {
		return nil
	}
	for index, arg := range args {
		if index+1 >= len(args) {
			break
		}
		switch arg {
		case "-S":
			if f.chains[args[index+1]] {
				return nil
			}
			return errors.New("chain not found")
		case "-C":
			return errors.New("rule not found")
		case "-N":
			if f.chains == nil {
				f.chains = make(map[string]bool)
			}
			f.chains[args[index+1]] = true
			return nil
		}
	}
	return nil
}

func (f *fakeRunner) contains(substrings ...string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		joined := strings.Join(append([]string{call.name}, call.args...), " ")
		matched := true
		for _, substring := range substrings {
			if !strings.Contains(joined, substring) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func testPortForwardPlugin(t *testing.T) (*PortForwardPlugin, *fakeRunner) {
	t.Helper()
	runner := &fakeRunner{}
	manager := newIPTablesManager(
		slog.New(slog.NewTextHandler(testDiscard{}, nil)),
		runner,
		func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("192.0.2.10")}, nil
		},
	)
	return newPortForwardPluginWithManager(manager), runner
}

type testDiscard struct{}

func (testDiscard) Write(p []byte) (int, error) { return len(p), nil }

func applyIptablesRules(t *testing.T, plugin *PortForwardPlugin, rules ...protocol.PortForwardRule) protocol.PortForwardResult {
	t.Helper()
	payload, err := json.Marshal(protocol.PortForwardPayload{Rules: rules})
	if err != nil {
		t.Fatal(err)
	}
	value, err := plugin.Execute(context.Background(), payload)
	if err != nil {
		t.Fatalf("execute port_forward: %v", err)
	}
	result, ok := value.(protocol.PortForwardResult)
	if !ok {
		t.Fatalf("unexpected result type %T", value)
	}
	return result
}

func TestPortForwardBuildsTCPAndUDPNATRules(t *testing.T) {
	plugin, runner := testPortForwardPlugin(t)
	defer plugin.Close()
	result := applyIptablesRules(t, plugin,
		protocol.PortForwardRule{
			RuleID: "tcp-web", ListenPort: 8080, Protocol: "tcp",
			TargetHost: "backend.example.com", TargetPort: 80, Enabled: true,
		},
		protocol.PortForwardRule{
			RuleID: "udp-dns", ListenPort: 5353, Protocol: "udp",
			TargetHost: "192.0.2.20", TargetPort: 53, Enabled: true,
		},
	)
	if result.ActiveRules != 2 || len(result.ListenPorts) != 2 || result.ListenPorts[0] != 5353 {
		t.Fatalf("result = %+v", result)
	}
	if !runner.contains("sysctl", "net.ipv4.ip_forward=1") {
		t.Fatal("IPv4 forwarding was not enabled")
	}
	if !runner.contains("iptables", "-t nat -A NODEHUB_PF -p tcp --dport 8080", "nodehub:tcp-web", "DNAT --to-destination 192.0.2.10:80") {
		t.Fatalf("TCP DNAT missing: %+v", runner.calls)
	}
	if !runner.contains("iptables", "-t nat -A NODEHUB_PF -p udp --dport 5353", "nodehub:udp-dns", "DNAT --to-destination 192.0.2.20:53") {
		t.Fatalf("UDP DNAT missing: %+v", runner.calls)
	}
	if !runner.contains("iptables", "-t nat -A NODEHUB_PF_POST", "-p udp -d 192.0.2.20 --dport 53", "nodehub:udp-dns", "MASQUERADE") {
		t.Fatalf("UDP MASQUERADE missing: %+v", runner.calls)
	}
}

func TestPortForwardFlushesAndDeletesRulesOnDisableAndClose(t *testing.T) {
	plugin, runner := testPortForwardPlugin(t)
	applyIptablesRules(t, plugin, protocol.PortForwardRule{
		RuleID: "pf-test", ListenPort: 8081, Protocol: "tcp",
		TargetHost: "192.0.2.10", TargetPort: 80, Enabled: true,
	})
	result := applyIptablesRules(t, plugin, protocol.PortForwardRule{
		RuleID: "pf-test", ListenPort: 8081, Protocol: "tcp",
		TargetHost: "192.0.2.10", TargetPort: 80, Enabled: false,
	})
	if result.ActiveRules != 0 {
		t.Fatalf("active rules = %d", result.ActiveRules)
	}
	if !runner.contains("iptables", "-t nat -F NODEHUB_PF") {
		t.Fatal("disabled rule did not flush the desired-state chain")
	}
	if err := plugin.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !runner.contains("iptables", "-t nat -D PREROUTING", "nodehub", "-j NODEHUB_PF") {
		t.Fatal("PREROUTING hook was not removed on close")
	}
	if !runner.contains("iptables", "-t nat -X NODEHUB_PF") {
		t.Fatal("custom chain was not deleted on close")
	}
}

func TestPortForwardRejectsInvalidProtocol(t *testing.T) {
	plugin, _ := testPortForwardPlugin(t)
	defer plugin.Close()
	_, err := plugin.Execute(context.Background(), []byte(`{"rules":[{"rule_id":"bad","listen_port":8080,"protocol":"sctp","target_host":"127.0.0.1","target_port":80,"enabled":true}]}`))
	if err == nil {
		t.Fatal("expected invalid protocol error")
	}
}
