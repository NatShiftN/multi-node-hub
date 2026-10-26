package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMockProviderReplacesIP(t *testing.T) {
	provider := &MockProvider{}
	ip, err := provider.ReplaceIP(context.Background(), "instance-1")
	if err != nil {
		t.Fatalf("ReplaceIP: %v", err)
	}
	if !strings.HasPrefix(ip, "198.51.100.") {
		t.Fatalf("unexpected mock IP %q", ip)
	}
}

func TestMockPowerAndBandwidth(t *testing.T) {
	provider := &MockProvider{}
	if err := provider.PowerOff(context.Background(), "instance-1"); err != nil {
		t.Fatal(err)
	}
	if err := provider.PowerOn(context.Background(), "instance-1"); err != nil {
		t.Fatal(err)
	}
	if err := provider.Reboot(context.Background(), "instance-1"); err != nil {
		t.Fatal(err)
	}
	bandwidth, err := provider.GetBandwidth(context.Background(), "instance-1")
	if err != nil || bandwidth.AllocatedBytes == 0 {
		t.Fatalf("bandwidth = %+v, err=%v", bandwidth, err)
	}
}

func TestVultrProviderRebuildFlow(t *testing.T) {
	rebuilt := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected authorization %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/instances/old-id":
			_ = json.NewEncoder(w).Encode(map[string]any{"instance": map[string]any{
				"id": "old-id", "region": "ewr", "plan": "vc2-1c-1gb", "os_id": 1743,
				"label": "node-1", "hostname": "node-1", "main_ip": "192.0.2.10",
			}})
		case r.Method == http.MethodDelete && r.URL.Path == "/instances/old-id":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/instances":
			rebuilt = true
			_ = json.NewEncoder(w).Encode(map[string]any{"instance": map[string]any{"id": "new-id"}})
		case r.Method == http.MethodGet && r.URL.Path == "/instances/new-id":
			if !rebuilt {
				t.Error("polled before create")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"instance": map[string]any{
				"id": "new-id", "main_ip": "192.0.2.55",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := NewVultrProvider("test-key", server.URL, nil)
	provider.PollInterval = 10 * time.Millisecond
	ip, newID, err := provider.ReplaceIPWithInstance(context.Background(), "old-id")
	if err != nil {
		t.Fatalf("ReplaceIPWithInstance: %v", err)
	}
	if ip != "192.0.2.55" || newID != "new-id" {
		t.Fatalf("ip=%q id=%q", ip, newID)
	}
}
