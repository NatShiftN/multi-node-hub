package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/controlplane"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "1.0.0-beta"

func main() {
	var (
		addr               = flag.String("addr", envOr("NODEHUB_ADDR", "127.0.0.1:8080"), "HTTP/WebSocket listen address")
		token              = flag.String("token", os.Getenv("AGENT_TOKEN"), "shared agent token (or AGENT_TOKEN)")
		webOrigin          = flag.String("web-origin", envOr("WEB_ORIGINS", "http://127.0.0.1:5173,http://localhost:5173"), "comma-separated allowed browser origins")
		commandTimeout     = flag.Duration("command-timeout", 15*time.Second, "maximum time to wait for an agent result")
		publicURL          = flag.String("public-url", envOr("NODEHUB_PUBLIC_URL", "http://127.0.0.1:8080"), "control-plane URL reachable from target machines")
		agentBinary        = flag.String("agent-binary", envOr("NODEHUB_AGENT_BINARY", "bin/nodehub-agent-linux-amd64"), "path to the Linux agent binary served to targets")
		agentARM64Binary   = flag.String("agent-arm64-binary", os.Getenv("NODEHUB_AGENT_ARM64_BINARY"), "optional Linux arm64 agent binary path")
		agentVersion       = flag.String("agent-version", envOr("AGENT_VERSION", version), "agent version bundled with this server")
		knownHosts         = flag.String("ssh-known-hosts", os.Getenv("SSH_KNOWN_HOSTS"), "SSH known_hosts file (empty disables host-key verification)")
		deployTimeout      = flag.Duration("deploy-timeout", 90*time.Second, "maximum time for the SSH bootstrap")
		maxDeploys         = flag.Int("max-concurrent-deploys", 4, "maximum concurrent SSH deployments")
		dbPath             = flag.String("db", envOr("NODEHUB_DB", "nodehub.db"), "SQLite database path")
		webDir             = flag.String("web-dir", envOr("NODEHUB_WEB_DIR", "web/dist"), "optional SPA static directory")
		adminUsername      = flag.String("admin-username", envOr("ADMIN_USERNAME", "admin"), "bootstrap admin username")
		adminPassword      = flag.String("admin-password", envOr("ADMIN_PASSWORD", "admin123"), "bootstrap admin password")
		jwtSecret          = flag.String("jwt-secret", os.Getenv("JWT_SECRET"), "JWT signing secret (empty: random per process)")
		jwtTTL             = flag.Duration("jwt-ttl", 24*time.Hour, "JWT lifetime")
		domain             = flag.String("domain", os.Getenv("NODEHUB_DOMAIN"), "public DNS domain; enables HTTPS/WSS via Let's Encrypt")
		email              = flag.String("email", os.Getenv("ACME_EMAIL"), "ACME account email")
		certDir            = flag.String("cert-dir", envOr("NODEHUB_CERT_DIR", "certcache"), "ACME certificate cache directory")
		httpAddr           = flag.String("http-addr", envOr("NODEHUB_HTTP_ADDR", ":80"), "HTTP redirect/ACME listen address when --domain is set")
		httpsAddr          = flag.String("https-addr", envOr("NODEHUB_HTTPS_ADDR", ":443"), "HTTPS/WSS listen address when --domain is set")
		probeInterval      = flag.Duration("ping-interval", time.Minute, "TCP probe interval for online nodes")
		probeTimeout       = flag.Duration("ping-timeout", 5*time.Second, "TCP probe connect timeout")
		probeThreshold     = flag.Int("ping-fail-threshold", 3, "consecutive failures before offline_network_error")
		probePort          = flag.Int("probe-port", 22, "TCP port probed on each node (SSH or proxy port)")
		quotaInterval      = flag.Duration("quota-interval", time.Minute, "traffic quota enforcement interval")
		cloudProvider      = flag.String("cloud-provider", envOr("CLOUD_PROVIDER", "mock"), "cloud provider: mock or vultr")
		vultrAPIKey        = flag.String("vultr-api-key", os.Getenv("VULTR_API_KEY"), "Vultr API key")
		vultrBaseURL       = flag.String("vultr-base-url", os.Getenv("VULTR_BASE_URL"), "Vultr API base URL override")
		doToken            = flag.String("digitalocean-token", os.Getenv("DIGITALOCEAN_TOKEN"), "DigitalOcean API token")
		doBaseURL          = flag.String("digitalocean-base-url", os.Getenv("DIGITALOCEAN_BASE_URL"), "DigitalOcean API base URL override")
		ddnsProvider       = flag.String("ddns-provider", envOr("DDNS_PROVIDER", "mock"), "DDNS provider: mock or cloudflare")
		cfAPIToken         = flag.String("cloudflare-api-token", os.Getenv("CLOUDFLARE_API_TOKEN"), "Cloudflare API token")
		cfBaseURL          = flag.String("cloudflare-base-url", os.Getenv("CLOUDFLARE_BASE_URL"), "Cloudflare API base URL override")
		autoHealEvery      = flag.Duration("auto-heal-cooldown", 30*time.Minute, "minimum interval between automatic heal attempts per node")
		gfwProbeURLs       = flag.String("gfw-probe-urls", os.Getenv("GFW_PROBE_URLS"), "comma-separated domestic TCP probe API URLs")
		gfwProbeToken      = flag.String("gfw-probe-token", os.Getenv("GFW_PROBE_TOKEN"), "domestic probe API bearer token")
		gfwProbeTimeout    = flag.Duration("gfw-probe-timeout", 10*time.Second, "domestic probe request timeout")
		gfwProbeThreshold  = flag.Int("gfw-probe-threshold", 2, "consecutive domestic blocked reports before gfw_blocked")
		gfwProbeMinReports = flag.Int("gfw-probe-min-reports", 1, "minimum successful domestic probe reports")
		webhookURL         = flag.String("webhook-url", os.Getenv("WEBHOOK_URL"), "notification webhook URL (Discord/Slack/generic)")
		tgBotToken         = flag.String("tg-bot-token", os.Getenv("TELEGRAM_BOT_TOKEN"), "Telegram bot token")
		tgChatID           = flag.String("tg-chat-id", os.Getenv("TELEGRAM_CHAT_ID"), "Telegram chat id")
		tgAdminID          = flag.String("tg-admin-id", os.Getenv("TELEGRAM_ADMIN_ID"), "Telegram numeric user ID allowed to control the bot")
		backupDir          = flag.String("backup-dir", envOr("NODEHUB_BACKUP_DIR", "backups"), "database backup directory")
		backupHour         = flag.Int("backup-hour", 4, "daily local-time hour for database backup (0-23)")
		demoMode           = flag.Bool("demo", false, "run in read-only demo mode with virtual nodes")
		showVersion        = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}
	if *token == "" {
		fmt.Fprintln(os.Stderr, "error: --token (or AGENT_TOKEN) is required")
		os.Exit(2)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	if os.Getenv(gin.EnvGinMode) == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	effectivePublicURL := *publicURL
	if *domain != "" && (effectivePublicURL == "" || effectivePublicURL == "http://127.0.0.1:8080") {
		effectivePublicURL = "https://" + *domain
	}
	origins := splitOrigins(*webOrigin)
	if *domain != "" {
		origins = appendUnique(origins, "https://"+*domain)
	}
	arm64Binary := strings.TrimSpace(*agentARM64Binary)
	if arm64Binary == "" {
		arm64Binary = strings.Replace(*agentBinary, "amd64", "arm64", 1)
	}

	server, err := controlplane.New(controlplane.Config{
		Demo:                  *demoMode,
		Addr:                  *addr,
		AgentToken:            *token,
		WebOrigins:            origins,
		CommandTimeout:        *commandTimeout,
		PingInterval:          25 * time.Second,
		PongWait:              60 * time.Second,
		PublicURL:             effectivePublicURL,
		AgentBinaryPath:       *agentBinary,
		AgentARM64BinaryPath:  arm64Binary,
		SSHKnownHostsFile:     *knownHosts,
		DeployTimeout:         *deployTimeout,
		MaxConcurrentDeploys:  *maxDeploys,
		DBPath:                *dbPath,
		WebDir:                *webDir,
		AgentVersion:          *agentVersion,
		AdminUsername:         *adminUsername,
		AdminPassword:         *adminPassword,
		JWTSecret:             *jwtSecret,
		JWTExpiration:         *jwtTTL,
		Domain:                *domain,
		Email:                 *email,
		CertDir:               *certDir,
		HTTPAddr:              *httpAddr,
		HTTPSAddr:             *httpsAddr,
		ProbeInterval:         *probeInterval,
		ProbeTimeout:          *probeTimeout,
		ProbeFailureThreshold: *probeThreshold,
		ProbePort:             *probePort,
		QuotaInterval:         *quotaInterval,
		CloudProviderName:     *cloudProvider,
		VultrAPIKey:           *vultrAPIKey,
		VultrBaseURL:          *vultrBaseURL,
		DigitalOceanToken:     *doToken,
		DigitalOceanBaseURL:   *doBaseURL,
		DDNSProviderName:      *ddnsProvider,
		CloudflareAPIToken:    *cfAPIToken,
		CloudflareBaseURL:     *cfBaseURL,
		AutoHealCooldown:      *autoHealEvery,
		GFWProbeURLs:          splitCSV(*gfwProbeURLs),
		GFWProbeToken:         *gfwProbeToken,
		GFWProbeTimeout:       *gfwProbeTimeout,
		GFWProbeThreshold:     *gfwProbeThreshold,
		GFWProbeMinReports:    *gfwProbeMinReports,
		WebhookURL:            *webhookURL,
		TelegramBotToken:      *tgBotToken,
		TelegramChatID:        *tgChatID,
		TelegramAdminID:       parseInt64(*tgAdminID),
		BackupDir:             *backupDir,
		BackupHour:            *backupHour,
	}, logger)
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(2)
	}
	defer func() {
		if err := server.Close(); err != nil {
			logger.Warn("close control plane", "error", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("nodehub control plane starting", "version", version)
	if err := server.Run(ctx); err != nil {
		logger.Error("control plane stopped", "error", err)
		os.Exit(1)
	}
	logger.Info("control plane stopped")
}

func splitOrigins(raw string) []string {
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		if origin := strings.TrimSpace(part); origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func parseInt64(raw string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			result = append(result, value)
		}
	}
	return result
}
