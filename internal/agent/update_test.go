package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nodehub/internal/protocol"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.9.0", "0.8.0", 1},
		{"v0.9.0", "0.9.0", 0},
		{"0.9.0", "0.9.1", -1},
		{"0.9.0", "0.9.0-dev", 1},
		{"0.10.0", "0.9.9", 1},
	}
	for _, test := range cases {
		if got := compareVersions(test.a, test.b); got != test.want {
			t.Fatalf("compareVersions(%q,%q)=%d want %d", test.a, test.b, got, test.want)
		}
	}
}

func TestUpdaterApplyDownloadsChecksAndReplaces(t *testing.T) {
	binary := []byte("new-agent-binary-content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(binary)
	}))
	defer server.Close()
	dir := t.TempDir()
	execPath := filepath.Join(dir, "nodehub-agent")
	if err := os.WriteFile(execPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	hasher := sha256.Sum256(binary)
	updater := NewUpdater("0.8.0", nil)
	updater.execPath = execPath
	updater.allowNonLinux = true
	restarted := false
	exited := false
	updater.restart = func(context.Context) error {
		restarted = true
		return nil
	}
	updater.exit = func(int) { exited = true }

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := updater.apply(ctx, protocol.AgentVersionInfo{
		Version:     "0.9.0",
		OS:          "linux",
		Arch:        "amd64",
		DownloadURL: server.URL,
		SHA256:      hex.EncodeToString(hasher[:]),
		Size:        int64(len(binary)),
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(binary) {
		t.Fatalf("binary content = %q", got)
	}
	if !restarted || !exited {
		t.Fatalf("restarted=%v exited=%v", restarted, exited)
	}
}

func TestUpdaterRejectsBadChecksum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("new"))
	}))
	defer server.Close()
	dir := t.TempDir()
	execPath := filepath.Join(dir, "nodehub-agent")
	if err := os.WriteFile(execPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	updater := NewUpdater("0.8.0", nil)
	updater.execPath = execPath
	updater.allowNonLinux = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := updater.apply(ctx, protocol.AgentVersionInfo{
		DownloadURL: server.URL,
		SHA256:      "deadbeef",
	})
	if err == nil {
		t.Fatal("expected checksum error")
	}
	got, _ := os.ReadFile(execPath)
	if string(got) != "old" {
		t.Fatalf("old binary was replaced: %q", got)
	}
}
