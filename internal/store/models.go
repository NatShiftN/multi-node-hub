package store

import "time"

const (
	NodeStatusOnline       = "online"
	NodeStatusOffline      = "offline"
	NodeStatusProvisioning = "provisioning"
	NodeStatusNetworkError = "offline_network_error"
	NodeStatusGFWBlocked   = "gfw_blocked"
)

const (
	RoleSuperAdmin = "superadmin"
	RoleOperator   = "operator"
	RoleViewer     = "viewer"
)

type AdminUser struct {
	ID           uint   `gorm:"primaryKey"`
	Username     string `gorm:"size:64;uniqueIndex;not null"`
	PasswordHash string `gorm:"size:255;not null"`
	Role         string `gorm:"size:24;not null;default:superadmin"`
	AllowedTags  string `gorm:"type:text;not null;default:'[]'"`
	TOTPSecret   string `gorm:"size:128;not null;default:''"`
	TOTPEnabled  bool   `gorm:"not null;default:false"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (AdminUser) TableName() string { return "admin_users" }

// Node is the durable view of a managed machine. Runtime WebSocket
// connections still live in the control-plane Hub.
type Node struct {
	NodeID             string `gorm:"column:node_id;primaryKey;size:64"`
	Name               string `gorm:"size:255;not null"`
	Address            string `gorm:"size:255;index"`
	RemoteIP           string `gorm:"size:64"`
	Status             string `gorm:"size:24;index;not null"`
	OS                 string `gorm:"size:32"`
	Arch               string `gorm:"size:32"`
	AgentVersion       string `gorm:"size:64"`
	Plugins            string `gorm:"type:text"`
	ConnectedAt        *time.Time
	LastSeen           *time.Time
	ProbeFailures      int `gorm:"not null;default:0"`
	NetworkErrorAt     *time.Time
	GFWFailures        int `gorm:"not null;default:0"`
	GFWBlockedAt       *time.Time
	CloudProvider      string `gorm:"size:32"`
	ProviderInstanceID string `gorm:"size:128"`
	DDNSDomain         string `gorm:"column:ddns_domain;size:255"`
	DDNSZoneID         string `gorm:"column:ddns_zone_id;size:64"`
	DDNSRecordID       string `gorm:"column:ddns_record_id;size:64"`
	AutoHeal           bool   `gorm:"not null;default:false"`
	NameManual         bool   `gorm:"not null;default:false"`
	LastAutoHealAt     *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (Node) TableName() string { return "nodes" }

// NodeHealEvent is the persistent circuit-breaker ledger for IP replacement and
// rebuild attempts. Keeping it in SQLite means a control-plane restart cannot
// reset the 24h quota.
type NodeHealEvent struct {
	ID        uint   `gorm:"primaryKey"`
	NodeID    string `gorm:"column:node_id;size:64;index;not null"`
	Action    string `gorm:"size:32;not null"`
	Reason    string `gorm:"size:255"`
	CreatedAt time.Time
}

func (NodeHealEvent) TableName() string { return "node_heal_events" }

type NotificationSetting struct {
	ID             uint   `gorm:"primaryKey"`
	TelegramToken  string `gorm:"size:255"`
	TelegramChatID string `gorm:"size:64"`
	WebhookURL     string `gorm:"size:1024"`
	UpdatedAt      time.Time
}

func (NotificationSetting) TableName() string { return "notification_settings" }

type CloudOrphan struct {
	ID         uint   `gorm:"primaryKey"`
	Provider   string `gorm:"size:32;not null;uniqueIndex:idx_cloud_orphan"`
	InstanceID string `gorm:"size:128;not null;uniqueIndex:idx_cloud_orphan"`
	Reason     string `gorm:"size:255"`
	Attempts   int    `gorm:"not null;default:0"`
	LastError  string `gorm:"type:text"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ResolvedAt *time.Time
}

func (CloudOrphan) TableName() string { return "cloud_orphans" }

type TraefikSetting struct {
	ID        uint   `gorm:"primaryKey"`
	APIURL    string `gorm:"size:1024"`
	Username  string `gorm:"size:255"`
	Password  string `gorm:"type:text"`
	LogPath   string `gorm:"size:1024"`
	UpdatedAt time.Time
}

func (TraefikSetting) TableName() string { return "traefik_settings" }

type NodeTag struct {
	NodeID    string `gorm:"column:node_id;primaryKey;size:64"`
	TagName   string `gorm:"column:tag_name;primaryKey;size:64;index"`
	CreatedAt time.Time
}

func (NodeTag) TableName() string { return "node_tags" }

// NodeDecommission blocks a deleted node ID from reconnecting with the shared
// AGENT_TOKEN. Per-node tokens are removed on delete, but the shared token is
// intentionally static and needs this tombstone.
type NodeDecommission struct {
	NodeID           string `gorm:"column:node_id;primaryKey;size:64"`
	DecommissionedAt time.Time
}

func (NodeDecommission) TableName() string { return "node_decommissions" }

type AuditLog struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Timestamp    time.Time `gorm:"index;not null" json:"timestamp"`
	Username     string    `gorm:"size:64;index;not null" json:"username"`
	Action       string    `gorm:"size:64;index;not null" json:"action"`
	TargetNodeID string    `gorm:"size:64;index" json:"target_node_id"`
	Details      string    `gorm:"type:text" json:"details"`
	IPAddress    string    `gorm:"size:64" json:"ip_address"`
}

func (AuditLog) TableName() string { return "audit_logs" }

type TagCount struct {
	TagName string `json:"tag"`
	Count   int64  `json:"count"`
}

// Token stores only the SHA-256 digest of a per-node deployment token.
// FirstUsedAt is nil until the token authenticates its first WebSocket.
type Token struct {
	NodeID      string `gorm:"column:node_id;primaryKey;size:64"`
	TokenHash   string `gorm:"size:64;uniqueIndex;not null"`
	CreatedAt   time.Time
	FirstUsedAt *time.Time
}

func (Token) TableName() string { return "tokens" }

// PortForwardRule is desired state. The Agent is the source of truth for
// whether a listener is currently active.
type PortForwardRule struct {
	RuleID     string `gorm:"column:rule_id;primaryKey;size:40"`
	NodeID     string `gorm:"column:node_id;size:64;index;uniqueIndex:idx_node_listen;not null"`
	ListenPort int    `gorm:"uniqueIndex:idx_node_listen;not null"`
	Protocol   string `gorm:"size:8;not null;default:tcp"`
	TargetHost string `gorm:"size:255;not null"`
	TargetPort int    `gorm:"not null"`
	Enabled    bool   `gorm:"index;not null"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (PortForwardRule) TableName() string { return "port_forward_rules" }

// ProxyInbound is the structured source of truth for a generated Xray
// inbound. SettingsJSON contains protocol/transport/security-specific fields
// and may contain credentials; it is never written to logs or audit records.
type ProxyInbound struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	NodeID       string    `gorm:"column:node_id;size:64;not null;uniqueIndex:idx_proxy_inbound_node_port,priority:1" json:"node_id"`
	Remark       string    `gorm:"size:128;not null" json:"remark"`
	Port         int       `gorm:"not null;uniqueIndex:idx_proxy_inbound_node_port,priority:2" json:"port"`
	Protocol     string    `gorm:"size:32;not null" json:"protocol"`
	Network      string    `gorm:"size:16;not null" json:"network"`
	Security     string    `gorm:"size:16;not null" json:"security"`
	SettingsJSON string    `gorm:"column:settings_json;type:text;not null" json:"settings_json"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (ProxyInbound) TableName() string { return "proxy_inbounds" }

// XrayConfig stores the complete Xray-core JSON document for a node.
type XrayConfig struct {
	NodeID    string `gorm:"column:node_id;primaryKey;size:64"`
	Config    string `gorm:"type:text;not null"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (XrayConfig) TableName() string { return "xray_configs" }

// UserTraffic stores accumulated billing counters. Raw* columns are the last
// cumulative counters reported by Xray and are used to derive deltas.
type UserTraffic struct {
	NodeID           string `gorm:"column:node_id;primaryKey;size:64"`
	UserEmail        string `gorm:"column:user_email;primaryKey;size:255"`
	UploadBytes      uint64 `gorm:"not null"`
	DownloadBytes    uint64 `gorm:"not null"`
	RawUploadBytes   uint64 `gorm:"not null"`
	RawDownloadBytes uint64 `gorm:"not null"`
	SessionID        string `gorm:"size:64"`
	LastUpdated      time.Time
}

func (UserTraffic) TableName() string { return "user_traffic" }

// TrafficDaily stores global daily deltas for dashboard month-to-date totals.
// It is intentionally small (one row per UTC day), not a per-user billing table.
type TrafficDaily struct {
	Day           string `gorm:"primaryKey;size:10"`
	UploadBytes   uint64 `gorm:"not null;default:0"`
	DownloadBytes uint64 `gorm:"not null;default:0"`
	UpdatedAt     time.Time
}

func (TrafficDaily) TableName() string { return "traffic_daily" }

// TrafficUser holds the cross-node quota and ban state for one Xray identity.
type TrafficUser struct {
	UserEmail   string `gorm:"column:user_email;primaryKey;size:255"`
	QuotaBytes  uint64 `gorm:"not null;default:0"`
	DeviceLimit int    `gorm:"not null;default:0"`
	IsBanned    bool   `gorm:"not null;default:false"`
	BannedAt    *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// APIKey is a permanent merchant credential. Only the SHA-256 digest is
// persisted; the plaintext sk-... token is returned exactly once at creation.
type APIKey struct {
	ID         uint   `gorm:"primaryKey"`
	Name       string `gorm:"size:128;not null"`
	KeyHash    string `gorm:"uniqueIndex;size:64;not null"`
	Prefix     string `gorm:"size:16;not null"`
	Revoked    bool   `gorm:"not null;default:false"`
	LastUsedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (APIKey) TableName() string { return "api_keys" }

// CloudCredential stores a provider API key. Only the control plane reads it;
// API responses must always mask it.
type CloudCredential struct {
	Provider  string `gorm:"primaryKey;size:32"`
	APIKey    string `gorm:"type:text;not null"`
	UpdatedAt time.Time
}

func (CloudCredential) TableName() string { return "cloud_credentials" }

type Certificate struct {
	ID        uint   `gorm:"primaryKey"`
	Domain    string `gorm:"uniqueIndex;size:255;not null"`
	CertPEM   string `gorm:"type:text;not null"`
	KeyPEM    string `gorm:"type:text;not null"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (Certificate) TableName() string { return "certificates" }

func (TrafficUser) TableName() string { return "traffic_users" }
