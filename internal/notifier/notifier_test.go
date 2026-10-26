package notifier

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebhookNotifier(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	notifier, err := NewWebhookNotifier(server.URL)
	if err != nil {
		t.Fatalf("NewWebhookNotifier: %v", err)
	}
	event := Event{
		Type:    "offline_network_error",
		Title:   "Node offline",
		Message: "probe failed",
		Fields:  map[string]string{"node": "edge-1", "ip": "198.51.100.1"},
	}
	if err := notifier.Send(context.Background(), event); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if payload["content"] == "" || payload["event"] != "offline_network_error" {
		t.Fatalf("payload = %+v", payload)
	}
	if !strings.Contains(payload["text"].(string), "edge-1") {
		t.Fatalf("text = %q", payload["text"])
	}
}

func TestTelegramNotifier(t *testing.T) {
	var path string
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	notifier, err := NewTelegramNotifier("token-123", "-100")
	if err != nil {
		t.Fatalf("NewTelegramNotifier: %v", err)
	}
	notifier.BaseURL = server.URL
	if err := notifier.Send(context.Background(), Event{Type: "auto_heal_success", Title: "Healed"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if path != "/bottoken-123/sendMessage" {
		t.Fatalf("path = %q", path)
	}
	if payload["chat_id"] != "-100" || !strings.Contains(payload["text"].(string), "Healed") {
		t.Fatalf("payload = %+v", payload)
	}
}
