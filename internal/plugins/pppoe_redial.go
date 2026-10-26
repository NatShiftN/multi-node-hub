package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	defaultPPPoEInterface = "ppp0"
	defaultPPPoEProvider  = "dsl-provider"
	defaultPublicIPURL    = "https://api.ipify.org"
)

var (
	pppoeInterfacePattern = regexp.MustCompile(`^[A-Za-z0-9_.:@-]{1,32}$`)
	pppoeProviderPattern  = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
)

type PPPoERedialConfig struct {
	Interface   string
	Provider    string
	PublicIPURL string
	Logger      *slog.Logger
}

type PPPoERedialResult struct {
	Redialed  bool   `json:"redialed"`
	OldIP     string `json:"old_ip,omitempty"`
	NewIP     string `json:"new_ip,omitempty"`
	Interface string `json:"interface,omitempty"`
	Provider  string `json:"provider"`
}

type PPPoERedialPlugin struct {
	interfaceName string
	provider      string
	publicIPURL   string
	logger        *slog.Logger
	client        *http.Client

	mu           sync.Mutex
	runCommand   func(ctx context.Context, name string, args ...string) error
	lookupIP     func(ctx context.Context) (string, error)
	pollInterval time.Duration
	waitTimeout  time.Duration
}

func NewPPPoERedialPlugin(cfg PPPoERedialConfig) (*PPPoERedialPlugin, error) {
	if cfg.Interface == "" {
		cfg.Interface = defaultPPPoEInterface
	}
	if cfg.Provider == "" {
		cfg.Provider = defaultPPPoEProvider
	}
	if cfg.PublicIPURL == "" {
		cfg.PublicIPURL = defaultPublicIPURL
	}
	if !pppoeInterfacePattern.MatchString(cfg.Interface) {
		return nil, errors.New("invalid PPPoE interface name")
	}
	if !pppoeProviderPattern.MatchString(cfg.Provider) {
		return nil, errors.New("invalid PPPoE provider name")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	plugin := &PPPoERedialPlugin{
		interfaceName: cfg.Interface,
		provider:      cfg.Provider,
		publicIPURL:   cfg.PublicIPURL,
		logger:        cfg.Logger,
		client:        &http.Client{Timeout: 8 * time.Second},
		pollInterval:  3 * time.Second,
		waitTimeout:   45 * time.Second,
	}
	plugin.runCommand = plugin.execCommand
	plugin.lookupIP = plugin.fetchPublicIP
	return plugin, nil
}

func (p *PPPoERedialPlugin) Name() string { return "pppoe_redial" }

func (p *PPPoERedialPlugin) Execute(ctx context.Context, payload json.RawMessage) (any, error) {
	var request struct {
		Reason string `json:"reason"`
	}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &request)
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	oldIP, _ := p.lookupIP(ctx)
	if p.interfaceName != "" {
		if err := p.runCommand(ctx, "ip", "link", "set", "dev", p.interfaceName, "down"); err != nil {
			p.logger.Warn("PPPoE interface down failed", "interface", p.interfaceName, "error", err)
		}
		time.Sleep(500 * time.Millisecond)
		if err := p.runCommand(ctx, "ip", "link", "set", "dev", p.interfaceName, "up"); err != nil {
			return nil, fmt.Errorf("bring up %s: %w", p.interfaceName, err)
		}
	}
	if err := p.runCommand(ctx, "poff", "-a"); err != nil {
		// poff returns non-zero when no session is active; pon can still work.
		p.logger.Debug("poff -a returned an error", "error", err)
	}
	time.Sleep(1 * time.Second)
	if err := p.runCommand(ctx, "pon", p.provider); err != nil {
		return nil, fmt.Errorf("pon %s: %w", p.provider, err)
	}
	newIP, err := p.waitForIPChange(ctx, oldIP)
	if err != nil {
		// The command may have succeeded but the public IP lookup is blocked by
		// the very network outage we are trying to repair. The server confirms
		// the new IP through the Agent reconnect path.
		p.logger.Warn("PPPoE redial issued; new IP not confirmed locally",
			"reason", request.Reason, "old_ip", oldIP, "error", err)
	}
	return PPPoERedialResult{
		Redialed:  true,
		OldIP:     oldIP,
		NewIP:     newIP,
		Interface: p.interfaceName,
		Provider:  p.provider,
	}, nil
}

func (p *PPPoERedialPlugin) waitForIPChange(ctx context.Context, oldIP string) (string, error) {
	deadline := time.Now().Add(p.waitTimeout)
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		current, err := p.lookupIP(ctx)
		if err == nil && current != "" && current != oldIP {
			return current, nil
		}
		if time.Now().After(deadline) {
			return "", errors.New("public IP did not change before timeout")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(p.pollInterval):
		}
	}
}

func (p *PPPoERedialPlugin) execCommand(ctx context.Context, name string, args ...string) error {
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
			return fmt.Errorf("%s: %w: %s", name, err, message)
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func (p *PPPoERedialPlugin) fetchPublicIP(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.publicIPURL, nil)
	if err != nil {
		return "", err
	}
	response, err := p.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 256))
	if err != nil {
		return "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("public IP HTTP %d", response.StatusCode)
	}
	value := strings.TrimSpace(string(body))
	ip := net.ParseIP(value)
	if ip == nil || ip.To4() == nil {
		return "", errors.New("public IP endpoint did not return an IPv4 address")
	}
	return ip.String(), nil
}
