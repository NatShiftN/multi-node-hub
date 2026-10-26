package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"nodehub/internal/protocol"
)

const (
	pfChain      = "NODEHUB_PF"
	pfPostChain  = "NODEHUB_PF_POST"
	pfComment    = "nodehub"
	maxRuleIDLen = 40
)

var forwardRuleIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`)

// PortForwardPlugin applies a complete IPTables NAT desired state. DNAT in
// PREROUTING preserves the original L3 packet for the target; MASQUERADE is
// used for routed targets so reply traffic returns through this node. The
// plugin never shells out through a string: every argument is validated and
// passed as a separate argv element.
type PortForwardPlugin struct {
	manager   *iptablesManager
	statePath string
}

func defaultPortForwardStatePath() string {
	if value := strings.TrimSpace(os.Getenv("NODEHUB_PORTFORWARD_STATE")); value != "" {
		return value
	}
	return "/var/lib/nodehub/portforward.json"
}

func NewPortForwardPlugin(logger *slog.Logger) *PortForwardPlugin {
	if logger == nil {
		logger = slog.Default()
	}
	return &PortForwardPlugin{
		manager:   newIPTablesManager(logger, execCommandRunner{}, defaultLookupIPv4),
		statePath: defaultPortForwardStatePath(),
	}
}

func newPortForwardPluginWithManager(manager *iptablesManager) *PortForwardPlugin {
	return &PortForwardPlugin{manager: manager}
}

func (p *PortForwardPlugin) Name() string { return "port_forward" }

func (p *PortForwardPlugin) Execute(ctx context.Context, payload json.RawMessage) (any, error) {
	var desired protocol.PortForwardPayload
	if err := json.Unmarshal(payload, &desired); err != nil {
		return nil, fmt.Errorf("decode port-forward rules: %w", err)
	}
	result, err := p.manager.Apply(ctx, desired.Rules)
	if err != nil {
		return nil, err
	}
	if err := p.persist(desired.Rules); err != nil {
		p.manager.logger.Warn("persist port-forward desired state", "error", err)
	}
	p.manager.logger.Info("iptables port-forward applied",
		"active_rules", result.ActiveRules,
		"listen_ports", result.ListenPorts,
	)
	return result, nil
}

func (p *PortForwardPlugin) persist(rules []protocol.PortForwardRule) error {
	if p.statePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p.statePath), 0o750); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(map[string]any{"rules": rules}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.statePath, raw, 0o600)
}

// Restore reapplies the last locally persisted rules without contacting the
// control plane. If the Agent restarts while Server is down, forwarding comes
// back from disk instead of waiting for a new desired state.
func (p *PortForwardPlugin) Restore(ctx context.Context) error {
	if p.statePath == "" {
		return nil
	}
	raw, err := os.ReadFile(p.statePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var desired struct {
		Rules []protocol.PortForwardRule `json:"rules"`
	}
	if err := json.Unmarshal(raw, &desired); err != nil {
		return err
	}
	if len(desired.Rules) == 0 {
		return nil
	}
	if _, err := p.manager.Apply(ctx, desired.Rules); err != nil {
		return err
	}
	p.manager.logger.Info("port-forward restored from local state", "rules", len(desired.Rules))
	return nil
}

func (p *PortForwardPlugin) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return p.manager.Close(ctx)
}

// ClosePreserve stops the plugin without flushing iptables. The Agent uses this
// on shutdown so a restart does not create a forwarding gap; the next process
// restores the persisted desired state before reconnecting.
func (p *PortForwardPlugin) ClosePreserve() error {
	p.manager.mu.Lock()
	defer p.manager.mu.Unlock()
	p.manager.closed = true
	return nil
}

type commandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type lookupFunc func(ctx context.Context, host string) ([]net.IP, error)

type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, name string, args ...string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("%s not found: %w", name, err)
	}
	command := exec.CommandContext(ctx, path, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if len(message) > 512 {
			message = message[:512]
		}
		if message != "" {
			return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, message)
		}
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

type iptablesManager struct {
	mu            sync.Mutex
	logger        *slog.Logger
	runner        commandRunner
	lookup        lookupFunc
	closed        bool
	forwardingSet bool
	chainReady    bool
}

func newIPTablesManager(logger *slog.Logger, runner commandRunner, lookup lookupFunc) *iptablesManager {
	if logger == nil {
		logger = slog.Default()
	}
	if lookup == nil {
		lookup = defaultLookupIPv4
	}
	return &iptablesManager{logger: logger, runner: runner, lookup: lookup}
}

func (m *iptablesManager) Apply(ctx context.Context, input []protocol.PortForwardRule) (protocol.PortForwardResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return protocol.PortForwardResult{}, errors.New("port-forward manager is closed")
	}
	normalized, err := normalizeRules(input)
	if err != nil {
		return protocol.PortForwardResult{}, err
	}
	if len(normalized) == 0 {
		if err := m.ensureChains(ctx); err != nil {
			return protocol.PortForwardResult{}, err
		}
		if err := m.flushChains(ctx); err != nil {
			return protocol.PortForwardResult{}, err
		}
		return protocol.PortForwardResult{ListenPorts: []int{}}, nil
	}
	if err := m.ensureForwarding(ctx); err != nil {
		return protocol.PortForwardResult{}, err
	}
	if err := m.ensureChains(ctx); err != nil {
		return protocol.PortForwardResult{}, err
	}
	if err := m.flushChains(ctx); err != nil {
		return protocol.PortForwardResult{}, err
	}

	ports := make([]int, 0, len(normalized))
	for _, rule := range normalized {
		if !rule.Enabled {
			continue
		}
		targetIP, err := m.resolveIPv4(ctx, rule.TargetHost)
		if err != nil {
			_ = m.flushChains(ctx)
			return protocol.PortForwardResult{}, fmt.Errorf("resolve target %s: %w", rule.TargetHost, err)
		}
		comment := pfComment + ":" + rule.RuleID
		destination := net.JoinHostPort(targetIP, strconv.Itoa(rule.TargetPort))
		dnat := []string{
			"-w", "5", "-t", "nat", "-A", pfChain,
			"-p", rule.Protocol,
			"--dport", strconv.Itoa(rule.ListenPort),
			"-m", "comment", "--comment", comment,
			"-j", "DNAT", "--to-destination", destination,
		}
		if err := m.run(ctx, "iptables", dnat...); err != nil {
			_ = m.flushChains(ctx)
			return protocol.PortForwardResult{}, fmt.Errorf("install DNAT for %s: %w", rule.RuleID, err)
		}
		// A locally hosted target already returns through this node, so
		// MASQUERADE is unnecessary and would hide the original client source
		// IP. Routed targets need it for the return path.
		if !isLocalIPv4(targetIP) {
			masq := []string{
				"-w", "5", "-t", "nat", "-A", pfPostChain,
				"-p", rule.Protocol,
				"-d", targetIP,
				"--dport", strconv.Itoa(rule.TargetPort),
				"-m", "comment", "--comment", comment,
				"-j", "MASQUERADE",
			}
			if err := m.run(ctx, "iptables", masq...); err != nil {
				_ = m.flushChains(ctx)
				return protocol.PortForwardResult{}, fmt.Errorf("install MASQUERADE for %s: %w", rule.RuleID, err)
			}
		}
		ports = append(ports, rule.ListenPort)
	}
	sort.Ints(ports)
	return protocol.PortForwardResult{ActiveRules: len(ports), ListenPorts: ports}, nil
}

func (m *iptablesManager) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	return m.cleanup(ctx)
}

func (m *iptablesManager) ensureForwarding(ctx context.Context) error {
	if m.forwardingSet {
		return nil
	}
	if err := m.run(ctx, "sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return fmt.Errorf("enable IPv4 forwarding: %w", err)
	}
	m.forwardingSet = true
	return nil
}

func (m *iptablesManager) ensureChains(ctx context.Context) error {
	if !m.chainReady {
		if err := m.ensureChain(ctx, pfChain); err != nil {
			return err
		}
		if err := m.ensureChain(ctx, pfPostChain); err != nil {
			return err
		}
		m.chainReady = true
	}
	if err := m.ensureJump(ctx, "PREROUTING", pfChain); err != nil {
		return err
	}
	if err := m.ensureJump(ctx, "POSTROUTING", pfPostChain); err != nil {
		return err
	}
	return nil
}

func (m *iptablesManager) ensureChain(ctx context.Context, chain string) error {
	if err := m.run(ctx, "iptables", "-w", "5", "-t", "nat", "-S", chain); err == nil {
		return nil
	}
	if err := m.run(ctx, "iptables", "-w", "5", "-t", "nat", "-N", chain); err != nil {
		return fmt.Errorf("create chain %s: %w", chain, err)
	}
	return nil
}

func (m *iptablesManager) ensureJump(ctx context.Context, parent, chain string) error {
	check := []string{"-w", "5", "-t", "nat", "-C", parent, "-m", "comment", "--comment", pfComment, "-j", chain}
	if err := m.run(ctx, "iptables", check...); err == nil {
		return nil
	}
	add := []string{"-w", "5", "-t", "nat", "-A", parent, "-m", "comment", "--comment", pfComment, "-j", chain}
	if err := m.run(ctx, "iptables", add...); err != nil {
		return fmt.Errorf("hook %s -> %s: %w", parent, chain, err)
	}
	return nil
}

func (m *iptablesManager) flushChains(ctx context.Context) error {
	for _, chain := range []string{pfChain, pfPostChain} {
		if err := m.run(ctx, "iptables", "-w", "5", "-t", "nat", "-F", chain); err != nil {
			return fmt.Errorf("flush chain %s: %w", chain, err)
		}
	}
	return nil
}

func (m *iptablesManager) cleanup(ctx context.Context) error {
	var errs []string
	for _, parentChain := range []string{"PREROUTING", "POSTROUTING"} {
		chain := pfChain
		if parentChain == "POSTROUTING" {
			chain = pfPostChain
		}
		_ = m.run(ctx, "iptables", "-w", "5", "-t", "nat", "-D", parentChain, "-m", "comment", "--comment", pfComment, "-j", chain)
	}
	for _, chain := range []string{pfChain, pfPostChain} {
		_ = m.run(ctx, "iptables", "-w", "5", "-t", "nat", "-F", chain)
		if err := m.run(ctx, "iptables", "-w", "5", "-t", "nat", "-X", chain); err != nil {
			errs = append(errs, fmt.Sprintf("delete chain %s: %v", chain, err))
		}
	}
	m.chainReady = false
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func defaultLookupIPv4(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip4", host)
}

func isLocalIPv4(raw string) bool {
	ip := net.ParseIP(raw)
	if ip == nil || ip.To4() == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsUnspecified() {
		return true
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, address := range addresses {
		network, ok := address.(*net.IPNet)
		if ok && network.IP.Equal(ip) {
			return true
		}
	}
	return false
}

func (m *iptablesManager) resolveIPv4(ctx context.Context, raw string) (string, error) {
	host := strings.TrimSpace(raw)
	if host == "" {
		return "", errors.New("target host is empty")
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4.String(), nil
		}
		return "", errors.New("IPTables port forwarding supports IPv4 targets only")
	}
	ips, err := m.lookup(ctx, host)
	if err != nil {
		return "", err
	}
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4.String(), nil
		}
	}
	return "", errors.New("target host has no IPv4 address")
}

func (m *iptablesManager) run(ctx context.Context, name string, args ...string) error {
	if m.runner == nil {
		return errors.New("command runner is not configured")
	}
	return m.runner.Run(ctx, name, args...)
}

func normalizeRules(input []protocol.PortForwardRule) ([]protocol.PortForwardRule, error) {
	result := make([]protocol.PortForwardRule, 0, len(input))
	seenPort := make(map[string]string, len(input))
	for _, rule := range input {
		rule.RuleID = strings.TrimSpace(rule.RuleID)
		if !forwardRuleIDPattern.MatchString(rule.RuleID) {
			return nil, errors.New("invalid port-forward rule id")
		}
		if rule.ListenPort < 1 || rule.ListenPort > 65535 {
			return nil, fmt.Errorf("rule %s has invalid listen port", rule.RuleID)
		}
		if rule.TargetPort < 1 || rule.TargetPort > 65535 {
			return nil, fmt.Errorf("rule %s has invalid target port", rule.RuleID)
		}
		protocolName := strings.ToLower(strings.TrimSpace(rule.Protocol))
		if protocolName == "" {
			protocolName = "tcp"
		}
		if protocolName != "tcp" && protocolName != "udp" {
			return nil, fmt.Errorf("rule %s has invalid protocol", rule.RuleID)
		}
		rule.Protocol = protocolName
		target, err := normalizeForwardTarget(rule.TargetHost)
		if err != nil {
			return nil, fmt.Errorf("rule %s: %w", rule.RuleID, err)
		}
		rule.TargetHost = target
		key := rule.Protocol + ":" + strconv.Itoa(rule.ListenPort)
		if previous, exists := seenPort[key]; exists {
			return nil, fmt.Errorf("rules %s and %s both listen on %s", previous, rule.RuleID, key)
		}
		seenPort[key] = rule.RuleID
		result = append(result, rule)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ListenPort == result[j].ListenPort {
			return result[i].RuleID < result[j].RuleID
		}
		return result[i].ListenPort < result[j].ListenPort
	})
	if len(result) > 128 {
		return nil, errors.New("too many port-forward rules")
	}
	return result, nil
}

func normalizeForwardTarget(raw string) (string, error) {
	const invalid = "target host must be an IP address or DNS hostname"
	host := strings.TrimSpace(raw)
	if host == "" {
		return "", errors.New("target host is required")
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" || len(host) > 253 {
		return "", errors.New(invalid)
	}
	ipCandidate := host
	if strings.HasPrefix(ipCandidate, "[") && strings.HasSuffix(ipCandidate, "]") {
		ipCandidate = ipCandidate[1 : len(ipCandidate)-1]
	}
	if ip := net.ParseIP(ipCandidate); ip != nil {
		return ip.String(), nil
	}
	if strings.ContainsAny(host, " \t\r\n/\\?#@:[]") {
		return "", errors.New(invalid)
	}
	host = strings.ToLower(host)
	labels := strings.Split(host, ".")
	numericLabels := len(labels) == 4
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return "", errors.New(invalid)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New(invalid)
		}
		for _, char := range label {
			switch {
			case char >= 'a' && char <= 'z':
				numericLabels = false
			case char >= '0' && char <= '9':
			case char == '-':
				numericLabels = false
			default:
				return "", errors.New(invalid)
			}
		}
	}
	if numericLabels {
		return "", errors.New("target host must be a valid IP address or DNS hostname")
	}
	return host, nil
}
