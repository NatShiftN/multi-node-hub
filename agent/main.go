package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nodehub/internal/agent"
	"nodehub/internal/plugins"
	"nodehub/internal/protocol"
)

// version is overridden at build time:
//
//	go build -ldflags "-X main.version=1.2.3" ./agent
var version = "1.0.0-beta"

func main() {
	var (
		serverURL      = flag.String("server", envOr("NODEHUB_SERVER", "ws://127.0.0.1:8080/ws/agent"), "control-plane WebSocket URL")
		token          = flag.String("token", os.Getenv("AGENT_TOKEN"), "shared agent token (or AGENT_TOKEN)")
		id             = flag.String("id", os.Getenv("NODE_ID"), "stable node ID (default: sanitized hostname)")
		commandTimeout = flag.Duration("command-timeout", 15*time.Second, "maximum plugin execution time")
		xrayDir        = flag.String("xray-dir", os.Getenv("XRAY_DATA_DIR"), "Xray data directory (default: platform specific)")
		xrayBinary     = flag.String("xray-binary", os.Getenv("XRAY_BINARY"), "existing Xray binary path (empty: download)")
		xrayURL        = flag.String("xray-download-url", os.Getenv("XRAY_DOWNLOAD_URL"), "Xray release ZIP URL or base URL")
		xrayStats      = flag.String("xray-stats-server", envOr("XRAY_STATS_SERVER", "127.0.0.1:10085"), "Xray Stats API server address")
		xrayStatsEvery = flag.Duration("xray-stats-interval", 30*time.Second, "Xray traffic stats polling interval")
		speedDownload  = flag.String("speedtest-download-url", os.Getenv("SPEEDTEST_DOWNLOAD_URL"), "speedtest download URL")
		speedUpload    = flag.String("speedtest-upload-url", os.Getenv("SPEEDTEST_UPLOAD_URL"), "speedtest upload URL")
		speedPing      = flag.String("speedtest-ping-addr", os.Getenv("SPEEDTEST_PING_ADDR"), "speedtest TCP ping host:port")
		pppoeInterface = flag.String("pppoe-interface", envOr("PPPOE_INTERFACE", "ppp0"), "PPPoE interface for adsl redial")
		pppoeProvider  = flag.String("pppoe-provider", envOr("PPPOE_PROVIDER", "dsl-provider"), "pon provider name for adsl redial")
		publicIPURL    = flag.String("public-ip-url", envOr("PUBLIC_IP_URL", "https://api.ipify.org"), "public IPv4 lookup endpoint")
		ddScriptURL    = flag.String("dd-script-url", envOr("DD_SCRIPT_URL", "https://raw.githubusercontent.com/MoeClub/Note/master/InstallNET.sh"), "allowlisted DD bootstrap script URL")
		ddScriptSHA    = flag.String("dd-script-sha256", os.Getenv("DD_SCRIPT_SHA256"), "optional DD script SHA-256")
		showVersion    = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	nodeID := *id
	if nodeID == "" {
		nodeID = agent.DefaultID()
	}
	if *token == "" {
		fmt.Fprintln(os.Stderr, "error: --token (or AGENT_TOKEN) is required")
		os.Exit(2)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	forwarder := plugins.NewPortForwardPlugin(logger)
	defer func() {
		if err := forwarder.ClosePreserve(); err != nil {
			logger.Warn("preserve port-forward rules on exit", "error", err)
		}
	}()

	events := make(chan protocol.Envelope, 32)
	xrayPlugin, err := plugins.NewXrayPlugin(plugins.XrayPluginConfig{
		DataDir:       *xrayDir,
		BinaryPath:    *xrayBinary,
		DownloadURL:   *xrayURL,
		StatsServer:   *xrayStats,
		StatsInterval: *xrayStatsEvery,
		Events:        events,
		Logger:        logger,
	})
	if err != nil {
		logger.Error("initialize Xray plugin", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := xrayPlugin.Close(); err != nil {
			logger.Warn("close Xray plugin", "error", err)
		}
	}()

	speedtestPlugin, err := plugins.NewSpeedtestPlugin(plugins.SpeedtestPluginConfig{
		DownloadURL: *speedDownload,
		UploadURL:   *speedUpload,
		PingAddr:    *speedPing,
		Logger:      logger,
	})
	if err != nil {
		logger.Error("initialize speedtest plugin", "error", err)
		os.Exit(1)
	}
	pppoePlugin, err := plugins.NewPPPoERedialPlugin(plugins.PPPoERedialConfig{
		Interface:   *pppoeInterface,
		Provider:    *pppoeProvider,
		PublicIPURL: *publicIPURL,
		Logger:      logger,
	})
	if err != nil {
		logger.Error("initialize pppoe_redial plugin", "error", err)
		os.Exit(1)
	}
	ddPlugin, err := plugins.NewDDPlugin(plugins.DDPluginConfig{
		ScriptURL:    *ddScriptURL,
		ScriptSHA256: *ddScriptSHA,
		Logger:       logger,
	})
	if err != nil {
		logger.Error("initialize dd plugin", "error", err)
		os.Exit(1)
	}

	registry, err := plugins.NewRegistry(
		plugins.SystemInfo{},
		forwarder,
		xrayPlugin,
		speedtestPlugin,
		pppoePlugin,
		ddPlugin,
		plugins.NewRebootPlugin(logger),
		plugins.NewCertPlugin(logger),
		plugins.NewWebPlugin(logger),
	)
	if err != nil {
		logger.Error("initialize plugins", "error", err)
		os.Exit(1)
	}

	client, err := agent.New(agent.Config{
		ServerURL:      *serverURL,
		Token:          *token,
		ID:             nodeID,
		Version:        version,
		CommandTimeout: *commandTimeout,
		ReconnectMin:   time.Second,
		ReconnectMax:   30 * time.Second,
		Events:         events,
	}, registry, logger)
	if err != nil {
		logger.Error("invalid agent configuration", "error", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("nodehub agent starting", "id", nodeID, "version", version)
	restoreCtx, restoreCancel := context.WithTimeout(ctx, 30*time.Second)
	if err := xrayPlugin.Restore(restoreCtx); err != nil {
		logger.Warn("restore local Xray state (continuing offline)", "error", err)
	}
	if err := forwarder.Restore(restoreCtx); err != nil {
		logger.Warn("restore local port-forward state (continuing offline)", "error", err)
	}
	restoreCancel()
	if err := client.Run(ctx); err != nil {
		logger.Error("agent stopped", "error", err)
		os.Exit(1)
	}
	logger.Info("agent stopped")
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
