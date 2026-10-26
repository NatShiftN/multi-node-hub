package controlplane

import (
	"encoding/json"
	"time"

	"nodehub/internal/store"
)

type NodeResponse struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Address            string     `json:"address"`
	RemoteIP           string     `json:"remote_ip,omitempty"`
	Status             string     `json:"status"`
	ConnectedAt        *time.Time `json:"connected_at,omitempty"`
	LastSeen           *time.Time `json:"last_seen,omitempty"`
	NetworkErrorAt     *time.Time `json:"network_error_at,omitempty"`
	GFWBlockedAt       *time.Time `json:"gfw_blocked_at,omitempty"`
	OnlineSeconds      int64      `json:"online_seconds"`
	AgentVersion       string     `json:"agent_version"`
	OS                 string     `json:"os"`
	Arch               string     `json:"arch"`
	Plugins            []string   `json:"plugins"`
	Tags               []string   `json:"tags"`
	CloudProvider      string     `json:"cloud_provider,omitempty"`
	ProviderInstanceID string     `json:"provider_instance_id,omitempty"`
	DDNSDomain         string     `json:"ddns_domain,omitempty"`
	DDNSZoneID         string     `json:"ddns_zone_id,omitempty"`
	DDNSRecordID       string     `json:"ddns_record_id,omitempty"`
	AutoHeal           bool       `json:"auto_heal"`
	NameManual         bool       `json:"name_manual"`
	LastAutoHealAt     *time.Time `json:"last_auto_heal_at,omitempty"`
	CPUPercent         float64    `json:"cpu_percent"`
	MemoryPercent      float64    `json:"memory_percent"`
	CPUHistory         []float64  `json:"cpu_history"`
	CreatedAt          time.Time  `json:"created_at"`
}

func nodeResponse(node store.Node, now time.Time, tags []string) NodeResponse {
	plugins := make([]string, 0)
	if node.Plugins != "" {
		_ = json.Unmarshal([]byte(node.Plugins), &plugins)
	}
	if plugins == nil {
		plugins = []string{}
	}

	onlineSeconds := int64(0)
	if node.Status == store.NodeStatusOnline && node.ConnectedAt != nil {
		onlineSeconds = int64(now.Sub(*node.ConnectedAt).Seconds())
		if onlineSeconds < 0 {
			onlineSeconds = 0
		}
	}
	return NodeResponse{
		ID:                 node.NodeID,
		Name:               node.Name,
		Address:            node.Address,
		RemoteIP:           node.RemoteIP,
		Status:             node.Status,
		ConnectedAt:        node.ConnectedAt,
		LastSeen:           node.LastSeen,
		NetworkErrorAt:     node.NetworkErrorAt,
		GFWBlockedAt:       node.GFWBlockedAt,
		OnlineSeconds:      onlineSeconds,
		AgentVersion:       node.AgentVersion,
		OS:                 node.OS,
		Arch:               node.Arch,
		Plugins:            plugins,
		Tags:               tags,
		CloudProvider:      node.CloudProvider,
		ProviderInstanceID: node.ProviderInstanceID,
		DDNSDomain:         node.DDNSDomain,
		DDNSZoneID:         node.DDNSZoneID,
		DDNSRecordID:       node.DDNSRecordID,
		AutoHeal:           node.AutoHeal,
		NameManual:         node.NameManual,
		LastAutoHealAt:     node.LastAutoHealAt,
		CreatedAt:          node.CreatedAt,
	}
}
