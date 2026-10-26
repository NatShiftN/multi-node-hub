package controlplane

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTokenStoreDeploymentTokenBinding(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	store := NewTokenStore("shared-token", 15*time.Minute, nil, nil)
	store.now = func() time.Time { return now }

	token, err := store.Issue("node-a")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if token == "" || len(token) < 32 {
		t.Fatalf("unexpected token length: %d", len(token))
	}
	if store.Authorize(token, "node-b") {
		t.Fatal("token must not authorize a different node id")
	}
	if !store.Authorize(token, "node-a") {
		t.Fatal("token should authorize its bound node id")
	}

	// Once connected, a token remains valid for reconnects even after the
	// initial provisioning window.
	now = now.Add(2 * time.Hour)
	if !store.Authorize(token, "node-a") {
		t.Fatal("connected token should survive the provisioning TTL")
	}
}

func TestTokenStorePendingTTL(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	store := NewTokenStore("shared-token", 5*time.Minute, nil, nil)
	store.now = func() time.Time { return now }

	token, err := store.Issue("node-a")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if store.PendingCount() != 1 {
		t.Fatalf("PendingCount = %d, want 1", store.PendingCount())
	}
	now = now.Add(6 * time.Minute)
	if store.Authorize(token, "node-a") {
		t.Fatal("expired pending token must not authorize")
	}
	if store.PendingCount() != 0 {
		t.Fatalf("PendingCount = %d, want 0 after expiry", store.PendingCount())
	}
}

func TestTokenStoreStaticTokenAndRevoke(t *testing.T) {
	store := NewTokenStore("shared-token", time.Minute, nil, nil)
	if !store.Authorize("shared-token", "manual-node") {
		t.Fatal("shared token should authorize a manual agent")
	}
	if store.Authorize("wrong-token", "manual-node") {
		t.Fatal("wrong shared token must not authorize")
	}

	token, err := store.Issue("node-a")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	store.Revoke(token)
	if store.Authorize(token, "node-a") {
		t.Fatal("revoked token must not authorize")
	}
}

func TestTokenStoreSurvivesServerRestart(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "agent")
	if err := os.WriteFile(binaryPath, []byte("fake"), 0o600); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	dbPath := filepath.Join(dir, "nodehub.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	newServer := func() *Server {
		t.Helper()
		server, err := New(Config{
			Addr:                  "127.0.0.1:0",
			AgentToken:            "shared-token",
			CommandTimeout:        5 * time.Second,
			PingInterval:          25 * time.Second,
			PongWait:              60 * time.Second,
			PublicURL:             "http://10.0.0.5:8080",
			AgentBinaryPath:       binaryPath,
			DeployTimeout:         2 * time.Second,
			MaxConcurrentDeploys:  2,
			DBPath:                dbPath,
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
		return server
	}

	first := newServer()
	token, err := first.tokens.Issue("node-persist")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first server: %v", err)
	}

	second := newServer()
	defer second.Close()
	if second.tokens.Authorize(token, "wrong-node") {
		t.Fatal("persisted token must remain bound to its node")
	}
	if !second.tokens.Authorize(token, "node-persist") {
		t.Fatal("persisted token should authorize after restart")
	}
}

func TestTokenStoreReissueRevokesOldToken(t *testing.T) {
	store := NewTokenStore("shared-token", time.Minute, nil, nil)
	first, err := store.Issue("node-a")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	second, err := store.Issue("node-a")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if store.Authorize(first, "node-a") {
		t.Fatal("reissuing a token must revoke the previous token")
	}
	if !store.Authorize(second, "node-a") {
		t.Fatal("latest token should authorize")
	}
}
