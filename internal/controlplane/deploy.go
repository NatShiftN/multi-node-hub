package controlplane

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	sshdeploy "nodehub/internal/deploy"
	"nodehub/scripts"
)

const (
	maxDeployBody       = 128 << 10
	maxPasswordBytes    = 1024
	defaultSSHPort      = 22
	agentDownloadPath   = "/downloads/nodehub-agent-linux-amd64"
	installScriptPath   = "/install.sh"
	deployNodeIDPrefix  = "node-"
	deployCommandMaxLen = 8 << 10
)

type deployRequest struct {
	Host string `json:"host"`
	// IP is a backward-compatible alias for Host.
	IP         string `json:"ip"`
	Port       int    `json:"port"`
	Password   string `json:"password"`
	PrivateKey string `json:"private_key"`
	Passphrase string `json:"passphrase"`
}

func (s *Server) handleDeploy(c *gin.Context) {
	started := time.Now()
	var req deployRequest
	defer func() {
		// Go strings are immutable; this drops the only reference controlled by
		// this handler so the value can be collected as soon as possible.
		req.Password = ""
	}()

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxDeployBody)
	decoder := json.NewDecoder(c.Request.Body)
	if err := decoder.Decode(&req); err != nil {
		if errors.Is(err, io.EOF) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "request body is required"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}

	req.Host = strings.TrimSpace(req.Host)
	legacyIP := strings.TrimSpace(req.IP)
	if req.Host != "" && legacyIP != "" && !strings.EqualFold(req.Host, legacyIP) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "host and ip must match when both are provided"})
		return
	}
	if req.Host == "" {
		req.Host = legacyIP
	}
	targetHost, err := normalizeDeployHost(req.Host)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Port == 0 {
		req.Port = defaultSSHPort
	}
	if req.Port < 1 || req.Port > 65535 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "port must be between 1 and 65535"})
		return
	}
	if req.Password == "" && strings.TrimSpace(req.PrivateKey) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password or SSH private key is required"})
		return
	}
	if len(req.Password) > maxPasswordBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password is too long"})
		return
	}
	if len(req.PrivateKey) > 64<<10 || len(req.Passphrase) > 1024 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSH private key or passphrase is too long"})
		return
	}
	if s.publicURLLoopback() && !isLoopbackHost(targetHost) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "control-plane --public-url points to loopback; remote targets cannot download the installer",
		})
		return
	}

	select {
	case s.deploySem <- struct{}{}:
		defer func() { <-s.deploySem }()
	default:
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many concurrent deployments; retry shortly"})
		return
	}

	if info, err := os.Stat(s.cfg.AgentBinaryPath); err != nil || info.IsDir() || info.Size() == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent binary is not available on the control plane"})
		return
	}

	nodeID, err := newDeployNodeID()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate node id"})
		return
	}
	nodeToken, err := s.tokens.Issue(nodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate node token"})
		return
	}
	defer func() {
		nodeToken = ""
	}()
	if err := s.store.CreateProvisioningNode(nodeID, targetHost, targetHost); err != nil {
		s.tokens.Revoke(nodeToken)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist node"})
		return
	}
	deploymentOK := false
	defer func() {
		if deploymentOK {
			return
		}
		s.tokens.Revoke(nodeToken)
		if err := s.store.DeleteNode(nodeID); err != nil {
			s.logger.Warn("remove failed deployment record", "node_id", nodeID, "error", err)
		}
	}()

	command, err := s.buildInstallCommand(nodeID, nodeToken)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build install command"})
		return
	}
	if len(command) > deployCommandMaxLen {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "install command is too long"})
		return
	}

	target := net.JoinHostPort(targetHost, strconv.Itoa(req.Port))
	ctx, cancel := context.WithTimeout(c.Request.Context(), s.cfg.DeployTimeout)
	defer cancel()

	password := req.Password
	req.Password = ""
	defer func() { password = "" }()
	privateKey := strings.TrimSpace(req.PrivateKey)
	passphrase := req.Passphrase
	req.PrivateKey = ""
	req.Passphrase = ""
	defer func() { privateKey = ""; passphrase = "" }()
	err = s.deployer.DeployNodeWithAuth(ctx, targetHost, req.Port, sshdeploy.Auth{
		Password: password, PrivateKey: privateKey, Passphrase: passphrase,
	}, command)
	if err != nil {
		safeError := redactSecrets(err.Error(), password, privateKey, passphrase, nodeToken)
		password = ""
		statusCode := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) {
			statusCode = http.StatusGatewayTimeout
		}
		s.logger.Warn("node deployment failed",
			"target", target,
			"node_id", nodeID,
			"duration_ms", time.Since(started).Milliseconds(),
			"error", safeError,
		)
		c.JSON(statusCode, gin.H{
			"error":   "deployment failed: " + safeError,
			"node_id": nodeID,
		})
		return
	}
	password = ""
	deploymentOK = true

	elapsed := time.Since(started)
	s.logger.Info("node deployment command completed",
		"target", target,
		"node_id", nodeID,
		"duration_ms", elapsed.Milliseconds(),
	)
	s.audit(c, "deploy", nodeID, map[string]any{
		"target": targetHost,
		"port":   req.Port,
	})
	c.JSON(http.StatusOK, gin.H{
		"status":     "provisioned",
		"node_id":    nodeID,
		"message":    "agent installed; waiting for its WebSocket connection",
		"elapsed_ms": elapsed.Milliseconds(),
	})
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}

func newDeployNodeID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return deployNodeIDPrefix + hex.EncodeToString(raw), nil
}

func (s *Server) buildInstallCommand(nodeID, nodeToken string) (string, error) {
	installURL, agentURL, wsURL, err := s.deploymentEndpoints()
	if err != nil {
		return "", err
	}
	args := strings.Join([]string{
		"--token", shellQuote(nodeToken),
		"--server", shellQuote(wsURL),
		"--id", shellQuote(nodeID),
		"--agent-url", shellQuote(agentURL),
	}, " ")

	// Download the script to a temp file first so a failed curl/wget cannot be
	// masked by a successful `bash -s` reading an empty pipe.
	command := fmt.Sprintf(
		`set -eu; tmp=$(mktemp /tmp/nodehub-install.XXXXXX); trap 'rm -f "$tmp"' EXIT; `+
			`if command -v curl >/dev/null 2>&1; then curl -fsSL --connect-timeout 10 --max-time 60 %s -o "$tmp"; `+
			`elif command -v wget >/dev/null 2>&1; then wget -q --timeout=60 -O "$tmp" %s; `+
			`else echo 'curl or wget is required' >&2; exit 127; fi; bash "$tmp" %s`,
		shellQuote(installURL), shellQuote(installURL), args,
	)
	return command, nil
}

func (s *Server) deploymentEndpoints() (installURL, agentURL, wsURL string, err error) {
	base, err := url.Parse(s.cfg.PublicURL)
	if err != nil {
		return "", "", "", err
	}
	base.Path = ""
	base.RawPath = ""
	base.RawQuery = ""
	base.Fragment = ""
	root := strings.TrimRight(base.String(), "/")

	wsBase := *base
	if wsBase.Scheme == "https" {
		wsBase.Scheme = "wss"
	} else {
		wsBase.Scheme = "ws"
	}
	wsBase.Path = "/ws/agent"

	return root + installScriptPath, root + agentDownloadPath, wsBase.String(), nil
}

// normalizeDeployHost accepts a literal IPv4/IPv6 address or an ASCII DNS
// hostname. Hostnames are lowercased and a trailing root dot is removed.
func normalizeDeployHost(raw string) (string, error) {
	const invalid = "host must be an IP address or DNS hostname"

	host := strings.TrimSpace(raw)
	if host == "" {
		return "", errors.New("host is required")
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
	allNumeric := len(labels) == 4
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return "", errors.New(invalid)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New(invalid)
		}
		if len(label) > 0 {
			for _, char := range label {
				switch {
				case char >= 'a' && char <= 'z':
				case char >= '0' && char <= '9':
				case char == '-':
				default:
					return "", errors.New(invalid)
				}
			}
		}
		for _, char := range label {
			if char < '0' || char > '9' {
				allNumeric = false
				break
			}
		}
	}
	if allNumeric {
		return "", errors.New("host must be a valid IP address or DNS hostname")
	}
	return host, nil
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) publicURLLoopback() bool {
	u, err := url.Parse(s.cfg.PublicURL)
	if err != nil {
		return false
	}
	return isLoopbackHost(u.Hostname())
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func redactSecrets(message string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
	}
	return message
}

func (s *Server) handleInstallScript(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "text/x-shellscript; charset=utf-8", scripts.InstallScript)
}

func (s *Server) handleAgentBinary(c *gin.Context) {
	s.serveAgentBinary(c, s.cfg.AgentBinaryPath, "nodehub-agent-linux-amd64")
}

func (s *Server) handleAgentARM64Binary(c *gin.Context) {
	s.serveAgentBinary(c, s.cfg.AgentARM64BinaryPath, "nodehub-agent-linux-arm64")
}

func (s *Server) serveAgentBinary(c *gin.Context, path, filename string) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent binary not found"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.Header("Content-Type", "application/octet-stream")
	c.File(path)
}
