package controlplane

import (
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	Demo                 bool
	Addr                 string
	WebDir               string
	AgentToken           string
	WebOrigins           []string
	CommandTimeout       time.Duration
	PingInterval         time.Duration
	PongWait             time.Duration
	PublicURL            string
	AgentBinaryPath      string
	AgentARM64BinaryPath string
	SSHKnownHostsFile    string
	DeployTimeout        time.Duration
	MaxConcurrentDeploys int
	DBPath               string
	AgentVersion         string

	AdminUsername string
	AdminPassword string
	JWTSecret     string
	JWTExpiration time.Duration

	ProbeInterval         time.Duration
	ProbeTimeout          time.Duration
	ProbeFailureThreshold int
	ProbePort             int
	QuotaInterval         time.Duration

	CloudProviderName   string
	VultrAPIKey         string
	VultrBaseURL        string
	DigitalOceanToken   string
	DigitalOceanBaseURL string
	DDNSProviderName    string
	CloudflareAPIToken  string
	CloudflareBaseURL   string

	AutoHealCooldown   time.Duration
	GFWProbeURLs       []string
	GFWProbeToken      string
	GFWProbeTimeout    time.Duration
	GFWProbeThreshold  int
	GFWProbeMinReports int
	WebhookURL         string
	TelegramBotToken   string
	TelegramChatID     string
	TelegramAdminID    int64
	BackupDir          string
	BackupHour         int

	Domain    string
	Email     string
	CertDir   string
	HTTPAddr  string
	HTTPSAddr string
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Addr) == "" {
		return errors.New("listen address must not be empty")
	}
	if c.AgentToken == "" {
		return errors.New("agent token must not be empty")
	}
	if c.CommandTimeout <= 0 {
		return errors.New("command timeout must be positive")
	}
	if c.PingInterval <= 0 {
		return errors.New("ping interval must be positive")
	}
	if c.PongWait <= c.PingInterval {
		return errors.New("pong wait must be greater than ping interval")
	}
	if c.DeployTimeout <= 0 {
		return errors.New("deploy timeout must be positive")
	}
	if c.MaxConcurrentDeploys <= 0 {
		return errors.New("max concurrent deploys must be positive")
	}
	if strings.TrimSpace(c.AgentBinaryPath) == "" {
		return errors.New("agent binary path must not be empty")
	}
	if strings.TrimSpace(c.DBPath) == "" {
		return errors.New("database path must not be empty")
	}
	if strings.TrimSpace(c.AgentVersion) == "" {
		return errors.New("agent version must not be empty")
	}
	if strings.TrimSpace(c.AdminUsername) == "" {
		return errors.New("admin username must not be empty")
	}
	if len(c.AdminPassword) < 8 {
		return errors.New("bootstrap admin password must be at least 8 characters")
	}
	if c.JWTExpiration <= 0 {
		return errors.New("JWT expiration must be positive")
	}
	if c.JWTSecret != "" && len(c.JWTSecret) < 32 {
		return errors.New("JWT secret must be at least 32 bytes")
	}
	if c.ProbeInterval <= 0 || c.ProbeTimeout <= 0 {
		return errors.New("probe interval and timeout must be positive")
	}
	if c.ProbeFailureThreshold <= 0 {
		return errors.New("probe failure threshold must be positive")
	}
	if c.ProbePort < 1 || c.ProbePort > 65535 {
		return errors.New("probe port must be between 1 and 65535")
	}
	if c.QuotaInterval <= 0 {
		return errors.New("quota interval must be positive")
	}
	if strings.TrimSpace(c.CloudProviderName) == "" {
		return errors.New("cloud provider must not be empty")
	}

	if strings.TrimSpace(c.DDNSProviderName) == "" {
		return errors.New("DDNS provider must not be empty")
	}
	if strings.EqualFold(c.DDNSProviderName, "cloudflare") && strings.TrimSpace(c.CloudflareAPIToken) == "" {
		return errors.New("CLOUDFLARE_API_TOKEN is required for the cloudflare DDNS provider")
	}
	if c.AutoHealCooldown <= 0 {
		return errors.New("auto-heal cooldown must be positive")
	}
	if c.GFWProbeThreshold < 0 {
		return errors.New("GFW probe threshold must not be negative")
	}
	if c.GFWProbeMinReports < 0 {
		return errors.New("GFW probe minimum reports must not be negative")
	}
	if strings.TrimSpace(c.BackupDir) == "" {
		return errors.New("backup directory must not be empty")
	}
	if c.BackupHour < 0 || c.BackupHour > 23 {
		return errors.New("backup hour must be between 0 and 23")
	}
	if strings.TrimSpace(c.TelegramBotToken) == "" && strings.TrimSpace(c.TelegramChatID) != "" {
		return errors.New("Telegram chat id requires a bot token")
	}
	if strings.TrimSpace(c.TelegramBotToken) != "" && strings.TrimSpace(c.TelegramChatID) == "" && c.TelegramAdminID == 0 {
		return errors.New("Telegram bot token requires TELEGRAM_CHAT_ID or TELEGRAM_ADMIN_ID")
	}
	if raw := strings.TrimSpace(c.WebhookURL); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("webhook URL must be http(s)")
		}
	}
	if err := validatePublicURL(c.PublicURL); err != nil {
		return err
	}
	if c.Domain != "" {
		if err := validateDomain(c.Domain); err != nil {
			return err
		}
		if strings.TrimSpace(c.CertDir) == "" {
			return errors.New("certificate cache directory must not be empty")
		}
		if strings.TrimSpace(c.HTTPAddr) == "" || strings.TrimSpace(c.HTTPSAddr) == "" {
			return errors.New("HTTP/HTTPS listen addresses must not be empty")
		}
		if c.Email != "" {
			if _, err := mail.ParseAddress(c.Email); err != nil {
				return errors.New("ACME email is invalid")
			}
		}
	}
	return nil
}

func validateDomain(raw string) error {
	domain := strings.TrimSpace(raw)
	if domain == "" || len(domain) > 253 || strings.ContainsAny(domain, " /\\:@[]") {
		return errors.New("TLS domain must be a bare DNS hostname")
	}
	labels := strings.Split(strings.TrimSuffix(domain, "."), ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("TLS domain is invalid")
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' {
				return errors.New("TLS domain is invalid")
			}
		}
	}
	return nil
}

func validatePublicURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("parse public URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("public URL must use http:// or https://")
	}
	if u.Host == "" {
		return errors.New("public URL must include a host")
	}
	if u.User != nil {
		return errors.New("public URL must not contain credentials")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return errors.New("public URL must not contain a query or fragment")
	}
	if u.Path != "" && u.Path != "/" {
		return errors.New("public URL must not contain a path prefix")
	}
	return nil
}
