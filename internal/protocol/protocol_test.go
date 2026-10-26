package protocol

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestValidAgentID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{name: "simple", id: "edge-01", want: true},
		{name: "single character", id: "a", want: true},
		{name: "dots and underscores", id: "node_1.example", want: true},
		{name: "max length", id: strings.Repeat("a", 64), want: true},
		{name: "empty", id: "", want: false},
		{name: "leading dash", id: "-node", want: false},
		{name: "space", id: "node 1", want: false},
		{name: "slash", id: "node/1", want: false},
		{name: "non ascii", id: "節點一", want: false},
		{name: "too long", id: strings.Repeat("a", 65), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ValidAgentID(test.id); got != test.want {
				t.Fatalf("ValidAgentID(%q) = %v, want %v", test.id, got, test.want)
			}
		})
	}
}

func TestResultEncodesData(t *testing.T) {
	env := Result("req-1", "system_info", map[string]any{"cpu_cores": 8}, nil)
	if env.Type != TypeResult {
		t.Fatalf("type = %q, want %q", env.Type, TypeResult)
	}
	if env.RequestID != "req-1" || env.Plugin != "system_info" {
		t.Fatalf("unexpected metadata: %+v", env)
	}
	if env.Error != "" {
		t.Fatalf("unexpected error: %s", env.Error)
	}
	var data struct {
		CPUCores int `json:"cpu_cores"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if data.CPUCores != 8 {
		t.Fatalf("cpu_cores = %d, want 8", data.CPUCores)
	}
}

func TestResultEncodesExecutionError(t *testing.T) {
	env := Result("req-2", "missing", nil, errors.New("plugin not found"))
	if env.Error != "plugin not found" {
		t.Fatalf("error = %q", env.Error)
	}
	if env.Data != nil {
		t.Fatalf("data = %s, want nil", env.Data)
	}
}

func TestResultReportsMarshalError(t *testing.T) {
	env := Result("req-3", "bad", map[string]any{"channel": make(chan int)}, nil)
	if !strings.Contains(env.Error, "encode plugin result") {
		t.Fatalf("error = %q, want marshal failure", env.Error)
	}
}
