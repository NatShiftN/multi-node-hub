package ddns

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMockProvider(t *testing.T) {
	provider := &MockProvider{}
	err := provider.Update(context.Background(), Request{
		ZoneID: "zone-1", Domain: "node.example.com", IP: "198.51.100.10",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	last, attempts := provider.LastRequest()
	if attempts != 1 || last.IP != "198.51.100.10" {
		t.Fatalf("last=%+v attempts=%d", last, attempts)
	}
}

func TestCloudflareLookupAndUpdate(t *testing.T) {
	var updateBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cf-token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/zones/zone-1/dns_records":
			if r.URL.Query().Get("type") != "A" || r.URL.Query().Get("name") != "node.example.com" {
				t.Errorf("unexpected query %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"result":  []map[string]any{{"id": "record-1"}},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/zones/zone-1/dns_records/record-1":
			if err := json.NewDecoder(r.Body).Decode(&updateBody); err != nil {
				t.Errorf("decode body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{"id": "record-1"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := NewCloudflareProvider("cf-token", server.URL)
	err := provider.Update(context.Background(), Request{
		ZoneID: "zone-1", Domain: "node.example.com", IP: "198.51.100.20",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updateBody["type"] != "A" || updateBody["content"] != "198.51.100.20" || updateBody["name"] != "node.example.com" {
		t.Fatalf("update body = %+v", updateBody)
	}
}
