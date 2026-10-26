package gfwprobe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPProbe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		var request probeRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode: %v", err)
		}
		if request.Host != "node.example.com" || request.Port != 443 {
			t.Errorf("request = %+v", request)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"blocked": true, "reachable": false, "detail": "tcp reset",
		})
	}))
	defer server.Close()
	probe, err := NewHTTPProbe(server.URL, "secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := probe.Check(context.Background(), "node.example.com", 443)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !result.Blocked || result.Reachable || result.Detail != "tcp reset" {
		t.Fatalf("result = %+v", result)
	}
}

func TestMultiRequiresMajority(t *testing.T) {
	blocked := &staticProbe{result: Result{Blocked: true, Reachable: false}}
	reachable := &staticProbe{result: Result{Blocked: false, Reachable: true}}
	multi, err := NewMulti([]Probe{blocked, reachable}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := multi.Check(context.Background(), "x", 443); err == nil {
		t.Fatal("tie should not change state")
	}
	multi, _ = NewMulti([]Probe{blocked, blocked, reachable}, 2)
	result, err := multi.Check(context.Background(), "x", 443)
	if err != nil || !result.Blocked {
		t.Fatalf("result = %+v, err=%v", result, err)
	}
}

type staticProbe struct {
	result Result
	err    error
}

func (s *staticProbe) Name() string { return "static" }
func (s *staticProbe) Check(context.Context, string, int) (Result, error) {
	return s.result, s.err
}
