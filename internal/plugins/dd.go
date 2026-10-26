package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"nodehub/internal/protocol"
)

const (
	defaultDDURL      = "https://raw.githubusercontent.com/MoeClub/Note/master/InstallNET.sh"
	maxDDDownloadSize = 2 << 20
)

var (
	ddNodeIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	ddVersionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)
)

type DDPluginConfig struct {
	ScriptURL    string
	ScriptSHA256 string
	Logger       *slog.Logger
	HTTPClient   *http.Client
}

type DDPlugin struct {
	scriptURL string
	scriptSHA string
	logger    *slog.Logger
	client    *http.Client
	runScript func(ctx context.Context, path string, args ...string) error
}

func NewDDPlugin(cfg DDPluginConfig) (*DDPlugin, error) {
	rawURL := strings.TrimSpace(cfg.ScriptURL)
	if rawURL == "" {
		rawURL = defaultDDURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, errors.New("DD script URL must be https")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	plugin := &DDPlugin{
		scriptURL: parsed.String(),
		scriptSHA: strings.ToLower(strings.TrimSpace(cfg.ScriptSHA256)),
		logger:    cfg.Logger,
		client:    cfg.HTTPClient,
	}
	plugin.runScript = plugin.execScript
	return plugin, nil
}

func (p *DDPlugin) Name() string { return "dd" }

func (p *DDPlugin) Execute(ctx context.Context, payload json.RawMessage) (any, error) {
	var request protocol.DDPayload
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, fmt.Errorf("decode DD payload: %w", err)
	}
	if !ddNodeIDPattern.MatchString(request.NodeID) {
		return nil, errors.New("invalid node_id")
	}
	if request.Token == "" || len(request.Token) > 512 {
		return nil, errors.New("invalid agent token")
	}
	if request.AgentURL == "" || request.ServerURL == "" {
		return nil, errors.New("agent/server URL is required")
	}
	if err := validateDDURLs(request.AgentURL, request.ServerURL); err != nil {
		return nil, err
	}
	distro := strings.ToLower(strings.TrimSpace(request.Distro))
	if distro != "debian" && distro != "ubuntu" {
		return nil, errors.New("distro must be debian or ubuntu")
	}
	if !ddVersionPattern.MatchString(request.Version) {
		return nil, errors.New("invalid OS version")
	}
	if request.Arch != "amd64" && request.Arch != "arm64" {
		return nil, errors.New("arch must be amd64 or arm64")
	}
	if len(request.RootPassword) > 128 || strings.ContainsAny(request.RootPassword, "\r\n") {
		return nil, errors.New("invalid root password")
	}

	scriptPath, err := p.downloadScript(ctx)
	if err != nil {
		return nil, err
	}
	postInstall := buildDDPostInstall(request)
	encoded := base64.StdEncoding.EncodeToString([]byte(postInstall))

	args := []string{
		"-d", request.Version,
		"-v", map[string]string{"amd64": "64", "arm64": "arm64"}[request.Arch],
		"-a",
		"-cmd", encoded,
	}
	if request.RootPassword != "" {
		args = append(args, "-p", request.RootPassword)
	}
	p.logger.Warn("starting destructive one-click DD", "node_id", request.NodeID, "distro", distro, "version", request.Version)
	go func() {
		runCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		if err := p.runScript(runCtx, scriptPath, args...); err != nil {
			p.logger.Error("DD script failed", "node_id", request.NodeID, "error", err)
			return
		}
		p.logger.Info("DD script completed", "node_id", request.NodeID)
	}()
	return map[string]any{"accepted": true, "distro": distro, "version": request.Version, "arch": request.Arch}, nil
}

func (p *DDPlugin) downloadScript(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.scriptURL, nil)
	if err != nil {
		return "", err
	}
	response, err := p.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("download DD script: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("DD script HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxDDDownloadSize+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxDDDownloadSize {
		return "", errors.New("DD script exceeds size limit")
	}
	if p.scriptSHA != "" {
		digest := sha256.Sum256(body)
		if hex.EncodeToString(digest[:]) != p.scriptSHA {
			return "", errors.New("DD script checksum mismatch")
		}
	}
	file, err := os.CreateTemp("", "nodehub-installnet-*.sh")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Chmod(0o700); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func (p *DDPlugin) execScript(ctx context.Context, path string, args ...string) error {
	command := exec.CommandContext(ctx, "bash", append([]string{path}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if len(message) > 1024 {
			message = message[:1024]
		}
		if message != "" {
			return fmt.Errorf("bash %s: %w: %s", path, err, message)
		}
		return fmt.Errorf("bash %s: %w", path, err)
	}
	return nil
}

func validateDDURLs(agentURL, serverURL string) error {
	agent, err := url.Parse(agentURL)
	if err != nil || (agent.Scheme != "http" && agent.Scheme != "https") || agent.Host == "" {
		return errors.New("invalid agent URL")
	}
	server, err := url.Parse(serverURL)
	if err != nil || (server.Scheme != "ws" && server.Scheme != "wss") || server.Host == "" {
		return errors.New("invalid server URL")
	}
	return nil
}

func buildDDPostInstall(request protocol.DDPayload) string {
	var builder strings.Builder
	builder.WriteString("#!/bin/sh\nset -eu\n")
	builder.WriteString("export DEBIAN_FRONTEND=noninteractive\n")
	builder.WriteString("exec >>/var/log/nodehub-dd.log 2>&1\n")
	builder.WriteString("mkdir -p /usr/local/bin /etc/systemd/system\n")
	builder.WriteString("for i in $(seq 1 30); do if command -v curl >/dev/null 2>&1; then break; fi; apt-get update >/dev/null 2>&1 && apt-get install -y curl ca-certificates >/dev/null 2>&1 && break; sleep 5; done\n")
	builder.WriteString("for i in $(seq 1 20); do curl -fsSL --retry 5 --retry-delay 3 --retry-all-errors -o /usr/local/bin/nodehub-agent ")
	builder.WriteString(shellSingleQuote(request.AgentURL))
	builder.WriteString(" && break; sleep 5; done\n")
	builder.WriteString("test -s /usr/local/bin/nodehub-agent || { echo 'agent download failed' >&2; exit 1; }\n")
	builder.WriteString("chmod 0755 /usr/local/bin/nodehub-agent\n")
	builder.WriteString("cat >/etc/systemd/system/nodehub-agent.service <<'EOF'\n")
	builder.WriteString("[Unit]\nDescription=NodeHub Edge Agent\nAfter=network-online.target\nWants=network-online.target\n\n")
	builder.WriteString("[Service]\nType=simple\nExecStart=/usr/local/bin/nodehub-agent --server ")
	builder.WriteString(systemdQuote(request.ServerURL))
	builder.WriteString(" --token ")
	builder.WriteString(systemdQuote(request.Token))
	builder.WriteString(" --id ")
	builder.WriteString(systemdQuote(request.NodeID))
	builder.WriteString("\nRestart=always\nRestartSec=5s\n\n[Install]\nWantedBy=multi-user.target\nEOF\n")
	builder.WriteString("systemctl daemon-reload\nsystemctl enable --now nodehub-agent\n")
	return builder.String()
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func systemdQuote(value string) string {
	return strconv.Quote(value)
}
