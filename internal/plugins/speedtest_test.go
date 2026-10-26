package plugins

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nodehub/internal/protocol"
)

func TestSpeedtestPluginLocalServer(t *testing.T) {
	const downloadSize = 2 << 20
	mux := http.NewServeMux()
	mux.HandleFunc("/down", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(make([]byte, downloadSize))
	})
	mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	plugin, err := NewSpeedtestPlugin(SpeedtestPluginConfig{
		DownloadURL: server.URL + "/down",
		UploadURL:   server.URL + "/up",
		PingAddr:    server.Listener.Addr().String(),
	})
	if err != nil {
		t.Fatalf("NewSpeedtestPlugin: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	value, err := plugin.Execute(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	result, ok := value.(protocol.SpeedtestResult)
	if !ok {
		t.Fatalf("unexpected result type %T", value)
	}
	if result.DownloadBytes != downloadSize {
		t.Fatalf("download bytes = %d, want %d", result.DownloadBytes, downloadSize)
	}
	if result.UploadBytes != speedtestUploadBytes {
		t.Fatalf("upload bytes = %d, want %d", result.UploadBytes, speedtestUploadBytes)
	}
	if result.DownloadMbps <= 0 || result.UploadMbps <= 0 || result.PingMS < 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestSpeedtestPluginRejectsInvalidURL(t *testing.T) {
	if _, err := NewSpeedtestPlugin(SpeedtestPluginConfig{DownloadURL: "not-a-url"}); err == nil {
		t.Fatal("expected invalid download URL error")
	}
}
