package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	sshdeploy "nodehub/internal/deploy"
	"time"

	"github.com/gin-gonic/gin"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	gin.DefaultWriter = io.Discard
	os.Exit(m.Run())
}

type fakeDeployer struct {
	calls    int
	host     string
	password string
	command  string
	failWith func(password, command string) error
}

func (f *fakeDeployer) DeployNodeWithAuth(_ context.Context, host string, _ int, auth sshdeploy.Auth, command string) error {
	f.calls++
	f.host = host
	f.password = auth.Password
	f.command = command
	if f.failWith != nil {
		return f.failWith(auth.Password, command)
	}
	return nil
}

func newDeployTestServer(t *testing.T) (*Server, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "nodehub-agent-linux-amd64")
	if err := os.WriteFile(binaryPath, []byte("FAKE-AGENT-BINARY"), 0o600); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	server, err := New(Config{
		Addr:                  "127.0.0.1:0",
		AgentToken:            "shared-token",
		CommandTimeout:        5 * time.Second,
		PingInterval:          25 * time.Second,
		PongWait:              60 * time.Second,
		PublicURL:             "http://10.0.0.5:8080/",
		AgentBinaryPath:       binaryPath,
		DeployTimeout:         2 * time.Second,
		MaxConcurrentDeploys:  2,
		DBPath:                filepath.Join(dir, "test.db"),
		AgentVersion:          "0.9.0",
		AdminUsername:         "admin",
		AdminPassword:         "admin123",
		JWTSecret:             strings.Repeat("t", 32),
		JWTExpiration:         time.Hour,
		ProbeInterval:         time.Minute,
		ProbeTimeout:          2 * time.Second,
		ProbeFailureThreshold: 3,
		ProbePort:             22,
		QuotaInterval:         time.Minute,
		CloudProviderName:     "mock",
		DDNSProviderName:      "mock",
		AutoHealCooldown:      30 * time.Minute,
		BackupDir:             t.TempDir(),
		BackupHour:            4,
	}, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("close server: %v", err)
		}
	})
	return server, &logs
}

func postDeploy(t *testing.T, server *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/deploy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testAuthToken(t, server))
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	return recorder
}

func TestDeploySuccessNeverExposesSecrets(t *testing.T) {
	server, logs := newDeployTestServer(t)
	fake := &fakeDeployer{}
	server.deployer = fake
	const password = "SuperSecret!42"

	recorder := postDeploy(t, server, fmt.Sprintf(
		`{"host":"NODE.Example.COM.","port":22,"password":%q}`, password,
	))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if fake.calls != 1 {
		t.Fatalf("deploy calls = %d, want 1", fake.calls)
	}
	if fake.host != "node.example.com" {
		t.Fatalf("deploy host = %q, want normalized domain", fake.host)
	}
	if fake.password != password {
		t.Fatal("deployer did not receive the password")
	}
	if strings.Contains(fake.command, password) {
		t.Fatal("password must never be embedded in the install command")
	}

	var response struct {
		Status    string `json:"status"`
		NodeID    string `json:"node_id"`
		ElapsedMS int64  `json:"elapsed_ms"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "provisioned" || !strings.HasPrefix(response.NodeID, "node-") {
		t.Fatalf("unexpected response: %+v", response)
	}
	nodeRecord, err := server.store.GetNode(response.NodeID)
	if err != nil {
		t.Fatalf("deployment node was not persisted: %v", err)
	}
	if nodeRecord.Status != "provisioning" {
		t.Fatalf("node status = %q, want provisioning", nodeRecord.Status)
	}

	match := regexp.MustCompile(`--token '([^']+)'`).FindStringSubmatch(fake.command)
	if len(match) != 2 {
		t.Fatalf("token missing from install command: %s", fake.command)
	}
	token := match[1]
	if strings.Contains(recorder.Body.String(), token) {
		t.Fatal("NodeToken must never be returned to the browser")
	}
	if strings.Contains(logs.String(), token) {
		t.Fatal("NodeToken must never be logged")
	}
	if strings.Contains(logs.String(), password) {
		t.Fatal("password must never be logged")
	}
	if server.tokens.PendingCount() != 1 {
		t.Fatalf("pending token count = %d, want 1", server.tokens.PendingCount())
	}
	if !server.tokens.Authorize(token, response.NodeID) {
		t.Fatal("issued token should authorize the deployed node id")
	}
	if server.tokens.PendingCount() != 0 {
		t.Fatalf("pending token count = %d, want 0 after connect", server.tokens.PendingCount())
	}
}

func TestDeployFailureRedactsSecretsAndRevokesToken(t *testing.T) {
	server, logs := newDeployTestServer(t)
	fake := &fakeDeployer{
		failWith: func(password, command string) error {
			return fmt.Errorf("simulated failure password=%s command=%s", password, command)
		},
	}
	server.deployer = fake
	const password = "VerySecret!99"

	recorder := postDeploy(t, server, fmt.Sprintf(
		`{"ip":"203.0.113.9","port":2222,"password":%q}`, password,
	))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var failureResponse struct {
		NodeID string `json:"node_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &failureResponse); err != nil {
		t.Fatalf("decode failure response: %v", err)
	}
	if failureResponse.NodeID == "" {
		t.Fatal("failure response has no node id")
	}
	if _, err := server.store.GetNode(failureResponse.NodeID); err == nil {
		t.Fatal("failed deployment record was not rolled back")
	}

	match := regexp.MustCompile(`--token '([^']+)'`).FindStringSubmatch(fake.command)
	if len(match) != 2 {
		t.Fatal("token missing from install command")
	}
	token := match[1]
	for _, secret := range []string{password, token} {
		if strings.Contains(recorder.Body.String(), secret) {
			t.Fatalf("response leaked secret %q: %s", secret, recorder.Body.String())
		}
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("log leaked secret %q", secret)
		}
	}
	if !strings.Contains(recorder.Body.String(), "[REDACTED]") {
		t.Fatalf("expected redaction marker in response: %s", recorder.Body.String())
	}
	if server.tokens.PendingCount() != 0 {
		t.Fatalf("pending token count = %d, want 0 after failure", server.tokens.PendingCount())
	}
}

func TestDeployRejectsLoopbackPublicURLForRemoteTarget(t *testing.T) {
	server, _ := newDeployTestServer(t)
	fake := &fakeDeployer{}
	server.deployer = fake
	server.cfg.PublicURL = "http://127.0.0.1:8080"

	recorder := postDeploy(t, server, `{"host":"node.example.com","port":22,"password":"not-logged"}`)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body = %s", recorder.Code, recorder.Body.String())
	}
	if fake.calls != 0 {
		t.Fatal("deployer must not run when the public URL is unreachable")
	}
}

func TestDeployAcceptsLegacyIPField(t *testing.T) {
	server, _ := newDeployTestServer(t)
	fake := &fakeDeployer{}
	server.deployer = fake

	recorder := postDeploy(t, server, `{"ip":"localhost","port":22,"password":"legacy-field"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if fake.host != "localhost" {
		t.Fatalf("deploy host = %q, want localhost", fake.host)
	}
}

func TestNormalizeDeployHost(t *testing.T) {
	valid := map[string]string{
		"192.0.2.10":         "192.0.2.10",
		"2001:db8::1":        "2001:db8::1",
		"[2001:db8::1]":      "2001:db8::1",
		"NODE.Example.COM.":  "node.example.com",
		"node-1.example.com": "node-1.example.com",
		"localhost":          "localhost",
		"compute.internal":   "compute.internal",
	}
	for input, want := range valid {
		got, err := normalizeDeployHost(input)
		if err != nil {
			t.Fatalf("normalizeDeployHost(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("normalizeDeployHost(%q) = %q, want %q", input, got, want)
		}
	}

	invalid := []string{
		"",
		"http://node.example.com",
		"node.example.com:22",
		"-bad.example.com",
		"bad-.example.com",
		"bad..example.com",
		"bad_host.example.com",
		"999.999.999.999",
		"exa mple.com",
		strings.Repeat("a", 64) + ".example.com",
	}
	for _, input := range invalid {
		if got, err := normalizeDeployHost(input); err == nil {
			t.Fatalf("normalizeDeployHost(%q) = %q, want error", input, got)
		}
	}
}

func TestDeployValidationRejectsBadInputWithoutLeakingPassword(t *testing.T) {
	server, _ := newDeployTestServer(t)
	server.deployer = &fakeDeployer{}
	const password = "DoNotLeak!77"

	cases := []struct {
		name string
		body string
		want int
	}{
		{
			name: "invalid host",
			body: fmt.Sprintf(`{"host":"http://node.example.com","port":22,"password":%q}`, password),
			want: http.StatusBadRequest,
		},
		{
			name: "missing password",
			body: `{"host":"node.example.com","port":22}`,
			want: http.StatusBadRequest,
		},
		{
			name: "invalid port",
			body: fmt.Sprintf(`{"host":"node.example.com","port":70000,"password":%q}`, password),
			want: http.StatusBadRequest,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			recorder := postDeploy(t, server, test.body)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d, body = %s", recorder.Code, test.want, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), password) {
				t.Fatal("validation response leaked the password")
			}
		})
	}
}

func TestInstallScriptAndAgentBinaryRoutes(t *testing.T) {
	server, _ := newDeployTestServer(t)

	scriptRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(scriptRecorder, httptest.NewRequest(http.MethodGet, "/install.sh", nil))
	if scriptRecorder.Code != http.StatusOK {
		t.Fatalf("install.sh status = %d", scriptRecorder.Code)
	}
	script := scriptRecorder.Body.String()
	for _, expected := range []string{"--token", "systemctl", "/usr/local/bin/nodehub-agent"} {
		if !strings.Contains(script, expected) {
			t.Fatalf("install.sh missing %q", expected)
		}
	}
	if !strings.Contains(scriptRecorder.Header().Get("Content-Type"), "shellscript") {
		t.Fatalf("unexpected script content type: %s", scriptRecorder.Header().Get("Content-Type"))
	}

	binaryRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(binaryRecorder, httptest.NewRequest(http.MethodGet, "/downloads/nodehub-agent-linux-amd64", nil))
	if binaryRecorder.Code != http.StatusOK {
		t.Fatalf("agent binary status = %d", binaryRecorder.Code)
	}
	if binaryRecorder.Body.String() != "FAKE-AGENT-BINARY" {
		t.Fatalf("unexpected binary body: %q", binaryRecorder.Body.String())
	}
}

func TestDeploymentEndpointsAndShellQuote(t *testing.T) {
	server, _ := newDeployTestServer(t)
	installURL, agentURL, wsURL, err := server.deploymentEndpoints()
	if err != nil {
		t.Fatalf("deploymentEndpoints: %v", err)
	}
	if installURL != "http://10.0.0.5:8080/install.sh" {
		t.Fatalf("install URL = %q", installURL)
	}
	if agentURL != "http://10.0.0.5:8080/downloads/nodehub-agent-linux-amd64" {
		t.Fatalf("agent URL = %q", agentURL)
	}
	if wsURL != "ws://10.0.0.5:8080/ws/agent" {
		t.Fatalf("ws URL = %q", wsURL)
	}
	if got, want := shellQuote("a'b"), `'a'\''b'`; got != want {
		t.Fatalf("shellQuote = %q, want %q", got, want)
	}
}
