// Package protocol defines the single wire format shared by the control plane
// and every edge agent. Keep this package dependency-free so agents stay small.
package protocol

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"
)

const (
	// Version is bumped whenever the wire contract changes incompatibly.
	Version = "0.1.0"

	// MaxMessageBytes caps a single WebSocket frame. Plugin payloads are small
	// control messages, never file contents.
	MaxMessageBytes int64 = 1 << 20
)

// MessageType is the envelope discriminator.
type MessageType string

const (
	TypeHello           MessageType = "hello"
	TypeCommand         MessageType = "command"
	TypeResult          MessageType = "result"
	TypeTelemetryUpdate MessageType = "telemetry_update"
	TypeTrafficUpdate   MessageType = "traffic_update"
	TypeVersionInfo     MessageType = "version_info"
)

// AgentInfo is sent by an agent immediately after the WebSocket handshake.
type AgentInfo struct {
	ID        string    `json:"id"`
	Hostname  string    `json:"hostname"`
	OS        string    `json:"os"`
	Arch      string    `json:"arch"`
	Version   string    `json:"version"`
	Plugins   []string  `json:"plugins"`
	StartedAt time.Time `json:"started_at"`
}

// PortForwardRule is the desired state sent to the Agent's port_forward
// plugin. The Agent reconciles its listeners against the complete list.
type PortForwardRule struct {
	RuleID     string `json:"rule_id"`
	ListenPort int    `json:"listen_port"`
	Protocol   string `json:"protocol"`
	TargetHost string `json:"target_host"`
	TargetPort int    `json:"target_port"`
	Enabled    bool   `json:"enabled"`
}

type PortForwardPayload struct {
	Rules []PortForwardRule `json:"rules"`
}

type PortForwardResult struct {
	ActiveRules int   `json:"active_rules"`
	ListenPorts []int `json:"listen_ports"`
}

// XrayPayload is the desired state for the Agent's xray plugin. Config is the
// complete Xray-core JSON document.
type XrayPayload struct {
	Action  string          `json:"action,omitempty"`
	Enabled bool            `json:"enabled"`
	Config  json.RawMessage `json:"config,omitempty"`
}

type XrayApplyResult struct {
	Running    bool   `json:"running"`
	PID        int    `json:"pid,omitempty"`
	BinaryPath string `json:"binary_path,omitempty"`
	ConfigPath string `json:"config_path,omitempty"`
	Downloaded bool   `json:"downloaded"`
}

// Telemetry is pushed by the Agent every five seconds. Network counters are
// cumulative interface totals; rates are computed by the receiver.
type Telemetry struct {
	Timestamp        time.Time `json:"timestamp"`
	CPUPercent       float64   `json:"cpu_percent"`
	MemoryPercent    float64   `json:"memory_percent"`
	MemoryUsedBytes  uint64    `json:"memory_used_bytes"`
	MemoryTotalBytes uint64    `json:"memory_total_bytes"`
	NetRXBytes       uint64    `json:"net_rx_bytes"`
	NetTXBytes       uint64    `json:"net_tx_bytes"`
}

// TrafficUserStat contains cumulative Xray counters for one stats identity.
// The Agent does not reset Xray counters; the Server derives deltas from raw
// counter baselines so dropped WS frames do not lose bytes.
type TrafficUserStat struct {
	Email         string `json:"email"`
	UploadBytes   uint64 `json:"upload_bytes"`
	DownloadBytes uint64 `json:"download_bytes"`
}

type TrafficUpdate struct {
	SessionID string            `json:"session_id"`
	Timestamp time.Time         `json:"timestamp"`
	Users     []TrafficUserStat `json:"users"`
}

type DDPayload struct {
	NodeID       string `json:"node_id"`
	Token        string `json:"token"`
	ServerURL    string `json:"server_url"`
	AgentURL     string `json:"agent_url"`
	Distro       string `json:"distro"`
	Version      string `json:"version"`
	Arch         string `json:"arch"`
	RootPassword string `json:"root_password,omitempty"`
}

type AgentVersionInfo struct {
	Version     string `json:"version"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	DownloadURL string `json:"download_url"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
}

// SpeedtestResult is returned by the Agent speedtest plugin. Rates are in
// megabits per second and PingMS is the average TCP handshake latency.
type SpeedtestResult struct {
	PingMS        float64   `json:"ping_ms"`
	DownloadMbps  float64   `json:"download_mbps"`
	UploadMbps    float64   `json:"upload_mbps"`
	DownloadBytes uint64    `json:"download_bytes"`
	UploadBytes   uint64    `json:"upload_bytes"`
	Server        string    `json:"server"`
	Timestamp     time.Time `json:"timestamp"`
	DurationMS    int64     `json:"duration_ms"`
}

// Envelope is the only message shape on the agent <-> control-plane wire.
type Envelope struct {
	Type       MessageType       `json:"type"`
	RequestID  string            `json:"request_id,omitempty"`
	Plugin     string            `json:"plugin,omitempty"`
	Payload    json.RawMessage   `json:"payload,omitempty"`
	Data       json.RawMessage   `json:"data,omitempty"`
	Error      string            `json:"error,omitempty"`
	Agent      *AgentInfo        `json:"agent,omitempty"`
	Telemetry  *Telemetry        `json:"telemetry,omitempty"`
	Traffic    *TrafficUpdate    `json:"traffic,omitempty"`
	Version    *AgentVersionInfo `json:"version_info,omitempty"`
	TimeoutMS  int64             `json:"timeout_ms,omitempty"`
	SentAt     time.Time         `json:"sent_at"`
	DurationMS int64             `json:"duration_ms,omitempty"`
}

// Result builds a result envelope. Result encoding errors are reported in Error
// rather than dropping the response, so the control plane never hangs.
func Result(requestID, plugin string, data any, execErr error) *Envelope {
	env := &Envelope{
		Type:      TypeResult,
		RequestID: requestID,
		Plugin:    plugin,
		SentAt:    time.Now().UTC(),
	}
	if execErr != nil {
		env.Error = execErr.Error()
		return env
	}
	if data == nil {
		return env
	}
	raw, err := json.Marshal(data)
	if err != nil {
		env.Error = fmt.Sprintf("encode plugin result: %v", err)
		return env
	}
	env.Data = raw
	return env
}

var agentIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidAgentID limits IDs to log-safe, URL-path-safe characters.
func ValidAgentID(id string) bool {
	return agentIDPattern.MatchString(id)
}
