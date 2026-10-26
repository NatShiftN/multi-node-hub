package controlplane

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/acme/autocert"

	"nodehub/internal/cloud"
	"nodehub/internal/ddns"
	sshdeploy "nodehub/internal/deploy"
	"nodehub/internal/gfwprobe"
	"nodehub/internal/notifier"
	"nodehub/internal/protocol"
	"nodehub/internal/store"
)

type nodeDeployer interface {
	DeployNodeWithAuth(ctx context.Context, host string, port int, auth sshdeploy.Auth, installCommand string) error
}

type Server struct {
	cfg            Config
	hub            *Hub
	store          *store.Store
	tokens         *TokenStore
	telemetry      *telemetryHub
	auth           *authManager
	deployer       nodeDeployer
	cloudProviders *cloud.Registry
	ddnsProviders  *ddns.Registry
	notifications  *notifier.Service
	gfwProbe       gfwprobe.Probe
	agentRelease   *protocol.AgentVersionInfo

	speedtestRunning sync.Map
	autoHealInFlight sync.Map
	deploySem        chan struct{}
	logger           *slog.Logger
	upgrader         websocket.Upgrader
	nodeStateMu      sync.Mutex
	xrayInboundMu    sync.Mutex
	xraySyncMu       sync.Mutex
	closeOnce        sync.Once
	closeErr         error
}

func New(cfg Config, logger *slog.Logger) (*Server, error) {
	cfg.PublicURL = strings.TrimRight(strings.TrimSpace(cfg.PublicURL), "/")
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}

	persistent, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if err := persistent.MarkAllNodesOffline(); err != nil {
		_ = persistent.Close()
		return nil, fmt.Errorf("mark nodes offline: %w", err)
	}
	tokens := NewTokenStore(cfg.AgentToken, deployTokenTTL, persistent, logger)
	if err := tokens.Load(); err != nil {
		_ = persistent.Close()
		return nil, err
	}
	auth, err := newAuthManager(persistent, cfg, logger)
	if err != nil {
		_ = persistent.Close()
		return nil, err
	}
	cloudProviders := cloud.NewRegistry(
		&cloud.MockProvider{Logger: logger},
		cloud.NewVultrProvider(cfg.VultrAPIKey, cfg.VultrBaseURL, logger),
		cloud.NewDigitalOceanProvider(cfg.DigitalOceanToken, cfg.DigitalOceanBaseURL, logger),
	)
	if _, ok := cloudProviders.Get(cfg.CloudProviderName); !ok {
		_ = persistent.Close()
		return nil, fmt.Errorf("unknown cloud provider %q", cfg.CloudProviderName)
	}
	ddnsProviders := ddns.NewRegistry(
		&ddns.MockProvider{Logger: logger},
		ddns.NewCloudflareProvider(cfg.CloudflareAPIToken, cfg.CloudflareBaseURL),
	)
	if _, ok := ddnsProviders.Get(cfg.DDNSProviderName); !ok {
		_ = persistent.Close()
		return nil, fmt.Errorf("unknown DDNS provider %q", cfg.DDNSProviderName)
	}
	var notificationTargets []notifier.Notifier
	if strings.TrimSpace(cfg.WebhookURL) != "" {
		target, err := notifier.NewWebhookNotifier(cfg.WebhookURL)
		if err != nil {
			_ = persistent.Close()
			return nil, err
		}
		notificationTargets = append(notificationTargets, target)
	}
	if strings.TrimSpace(cfg.TelegramBotToken) != "" && strings.TrimSpace(cfg.TelegramChatID) != "" {
		target, err := notifier.NewTelegramNotifier(cfg.TelegramBotToken, cfg.TelegramChatID)
		if err != nil {
			_ = persistent.Close()
			return nil, err
		}
		notificationTargets = append(notificationTargets, target)
	}

	var domesticProbe gfwprobe.Probe
	if len(cfg.GFWProbeURLs) > 0 {
		probes := make([]gfwprobe.Probe, 0, len(cfg.GFWProbeURLs))
		for _, rawURL := range cfg.GFWProbeURLs {
			probe, err := gfwprobe.NewHTTPProbe(rawURL, cfg.GFWProbeToken, cfg.GFWProbeTimeout)
			if err != nil {
				_ = persistent.Close()
				return nil, err
			}
			probes = append(probes, probe)
		}
		if len(probes) == 1 {
			domesticProbe = probes[0]
		} else {
			multi, err := gfwprobe.NewMulti(probes, cfg.GFWProbeMinReports)
			if err != nil {
				_ = persistent.Close()
				return nil, err
			}
			domesticProbe = multi
		}
	}

	deployer, err := sshdeploy.New(sshdeploy.Config{
		DialTimeout:      10 * time.Second,
		HandshakeTimeout: 15 * time.Second,
		KnownHostsFile:   cfg.SSHKnownHostsFile,
		Logger:           logger,
	})
	if err != nil {
		_ = persistent.Close()
		return nil, err
	}

	if _, err := os.Stat(cfg.AgentBinaryPath); err != nil {
		logger.Warn("agent binary is not available; deployments and OTA will fail",
			"path", cfg.AgentBinaryPath,
			"error", err,
		)
	}
	agentRelease, releaseErr := buildAgentRelease(cfg)
	if releaseErr != nil {
		logger.Warn("compute agent release metadata", "error", releaseErr)
	}
	if cfg.Domain != "" {
		if err := os.MkdirAll(cfg.CertDir, 0o700); err != nil {
			_ = persistent.Close()
			return nil, fmt.Errorf("create certificate cache: %w", err)
		}
	}

	server := &Server{
		cfg:            cfg,
		hub:            NewHub(),
		store:          persistent,
		tokens:         tokens,
		telemetry:      newTelemetryHub(),
		auth:           auth,
		deployer:       deployer,
		cloudProviders: cloudProviders,
		ddnsProviders:  ddnsProviders,
		notifications:  notifier.NewService(logger, notificationTargets...),
		gfwProbe:       domesticProbe,
		agentRelease:   agentRelease,
		deploySem:      make(chan struct{}, cfg.MaxConcurrentDeploys),
		logger:         logger,
		upgrader: websocket.Upgrader{
			HandshakeTimeout: 10 * time.Second,
			ReadBufferSize:   4096,
			WriteBufferSize:  4096,
			// Agents are native processes and never send an Origin header.
			// Reject browser-originated WebSocket connections.
			CheckOrigin: func(r *http.Request) bool {
				return r.Header.Get("Origin") == ""
			},
		},
	}
	server.applyNotificationSettings()
	if cfg.Demo {
		if err := server.seedDemo(); err != nil {
			_ = persistent.Close()
			return nil, fmt.Errorf("seed demo data: %w", err)
		}
	}
	return server, nil
}

func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.hub.CloseAll()
		if s.store != nil {
			_ = s.store.MarkAllNodesOffline()
			s.closeErr = s.store.Close()
		}
	})
	return s.closeErr
}

func (s *Server) Handler() http.Handler {
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery(), corsMiddleware(s.cfg.WebOrigins))
	_ = router.SetTrustedProxies(nil)

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":              "ok",
			"nodes":               s.hub.Count(),
			"pending_deployments": s.tokens.PendingCount(),
			"time":                time.Now().UTC(),
		})
	})
	router.GET("/ws/agent", s.handleAgentWebSocket)
	router.GET("/install.sh", s.handleInstallScript)
	router.GET("/downloads/nodehub-agent-linux-amd64", s.handleAgentBinary)
	router.GET("/downloads/nodehub-agent-linux-arm64", s.handleAgentARM64Binary)
	router.POST("/api/login", s.auth.handleLogin)
	router.GET("/v1/openapi.json", s.handleOpenAPISpec)
	router.GET("/api/demo", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"demo": s.cfg.Demo})
	})

	v1 := router.Group("/v1")
	v1.Use(s.apiKeyMiddleware())
	v1.GET("/nodes", s.handleV1ListNodes)
	v1.GET("/nodes/:id", s.handleV1GetNode)
	v1.POST("/traffic/allocate", s.handleV1AllocateTraffic)
	v1.GET("/traffic/users/:email", s.handleV1GetTrafficUser)

	api := router.Group("/api")
	api.Use(s.auth.Middleware())
	if s.cfg.Demo {
		api.Use(s.demoGuard())
	}
	super := s.auth.RequireSuperAdmin()
	api.POST("/admin/password", s.auth.handleChangePassword)
	api.GET("/admin/2fa", s.handleTOTPStatus)
	api.POST("/admin/2fa/setup", s.handleTOTPSetup)
	api.POST("/admin/2fa/enable", s.handleTOTPEnable)
	api.POST("/admin/2fa/disable", s.handleTOTPDisable)
	api.GET("/admin/users", super, s.handleListAdminUsers)
	api.POST("/admin/users", super, s.handleCreateAdminUser)
	api.PATCH("/admin/users/:id", super, s.handleUpdateAdminUser)
	api.DELETE("/admin/users/:id", super, s.handleDeleteAdminUser)
	api.GET("/agent/version", s.handleAgentVersion)
	api.GET("/system/backup", super, s.handleBackupDownload)
	api.GET("/audit-logs", s.handleListAuditLogs)
	api.GET("/traefik/settings", super, s.handleGetTraefikSettings)
	api.PUT("/traefik/settings", super, s.handleSaveTraefikSettings)
	api.POST("/traefik/test", super, s.handleTestTraefik)
	api.GET("/traefik/routes", super, s.handleTraefikRoutes)
	api.GET("/traefik/logs", super, s.handleTraefikLogs)
	api.GET("/notifications/settings", super, s.handleGetNotificationSettings)
	api.PUT("/notifications/settings", super, s.handleSaveNotificationSettings)
	api.POST("/notifications/test", super, s.handleTestNotification)
	api.GET("/cloud/credentials", super, s.handleListCloudCredentials)
	api.PUT("/cloud/credentials", super, s.handleSetCloudCredential)
	api.POST("/cloud/credentials/test", super, s.handleTestCloudCredential)
	api.DELETE("/cloud/credentials/:provider", super, s.handleDeleteCloudCredential)
	api.GET("/api-keys", super, s.handleListAPIKeys)
	api.POST("/api-keys", super, s.handleCreateAPIKey)
	api.DELETE("/api-keys/:id", super, s.handleRevokeAPIKey)
	api.GET("/dashboard/stats", s.handleDashboardStats)
	api.GET("/certificates", s.handleListCertificates)
	api.POST("/certificates", super, s.handleCreateCertificate)
	api.DELETE("/certificates/:id", super, s.handleDeleteCertificate)
	api.GET("/site-templates", s.handleListSiteTemplates)
	api.POST("/nodes/:id/site", super, s.handleDeploySite)
	api.GET("/nodes", s.handleListNodes)
	api.GET("/nodes/:id", s.handleGetNode)
	api.PATCH("/nodes/:id/identity", super, s.handleUpdateNodeIdentity)
	api.DELETE("/nodes/:id", super, s.handleDeleteNode)
	api.PATCH("/nodes/:id/cloud", s.handleUpdateNodeCloud)
	api.POST("/nodes/:id/cloud/power", super, s.handleCloudPower)
	api.GET("/nodes/:id/cloud/bandwidth", s.handleCloudBandwidth)
	api.POST("/nodes/:id/replace-ip", super, s.handleReplaceIP)
	api.POST("/nodes/:id/dd", super, s.handleDDNode)
	api.GET("/tags", s.handleListTags)
	api.PUT("/nodes/:id/tags", s.handleSetNodeTags)
	api.POST("/batch/command", s.handleBatchCommand)
	api.GET("/telemetry", s.handleTelemetrySnapshot)
	api.GET("/events", s.handleTelemetryEvents)
	api.POST("/nodes/:id/command", s.handleCommand)
	api.GET("/nodes/:id/port-forwards", s.handleListPortForwards)
	api.POST("/nodes/:id/port-forwards", s.handleCreatePortForward)
	api.PATCH("/nodes/:id/port-forwards/:ruleId", s.handleUpdatePortForward)
	api.DELETE("/nodes/:id/port-forwards/:ruleId", s.handleDeletePortForward)
	api.GET("/nodes/:id/xray", s.handleGetXrayConfig)
	api.PUT("/nodes/:id/xray", s.handlePutXrayConfig)
	api.DELETE("/nodes/:id/xray", s.handleDeleteXrayConfig)
	api.GET("/nodes/:id/xray/status", s.handleXrayStatus)
	api.POST("/nodes/:id/xray/restart", super, s.handleXrayRestart)
	api.GET("/nodes/:id/inbounds", s.handleListProxyInbounds)
	api.POST("/nodes/:id/inbounds", s.handleCreateProxyInbound)
	api.PATCH("/nodes/:id/inbounds/:inboundId", s.handleUpdateProxyInbound)
	api.DELETE("/nodes/:id/inbounds/:inboundId", s.handleDeleteProxyInbound)
	api.POST("/nodes/:id/inbounds/reality-keys", s.handleRealityKeys)
	api.GET("/nodes/:id/inbounds/:inboundId/share", s.handleInboundShare)
	api.GET("/nodes/:id/subscription", s.handleNodeSubscription)
	api.GET("/nodes/:id/traffic", s.handleListUserTraffic)
	api.GET("/traffic/users", s.handleListTrafficUsers)
	api.GET("/traffic/history", s.handleTrafficHistory)
	api.PUT("/traffic/users/:email/quota", super, s.handleSetUserQuota)
	api.POST("/traffic/users/:email/ban", super, s.handleBanUser)
	api.POST("/traffic/users/:email/unban", super, s.handleUnbanUser)
	api.POST("/nodes/:id/speedtest", s.handleSpeedtest)
	api.POST("/deploy", super, s.handleDeploy)

	if dir := strings.TrimSpace(s.cfg.WebDir); dir != "" {
		index := filepath.Join(dir, "index.html")
		if info, err := os.Stat(index); err == nil && !info.IsDir() {
			router.Static("/assets", filepath.Join(dir, "assets"))
			router.StaticFile("/", index)
			router.NoRoute(func(c *gin.Context) {
				if strings.HasPrefix(c.Request.URL.Path, "/api/") || strings.HasPrefix(c.Request.URL.Path, "/v1/") {
					c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
					return
				}
				c.File(index)
			})
		}
	}
	return router
}

func (s *Server) Run(ctx context.Context) error {
	handler := s.Handler()
	runCtx, cancelRun := context.WithCancel(ctx)
	if s.cfg.Demo {
		go s.runDemoTelemetry(runCtx)
	}
	defer cancelRun()
	go s.persistHeartbeats(runCtx)
	if !s.cfg.Demo {
		go s.probeLoop(runCtx)
		go s.orphanCleanupLoop(runCtx)
	}
	go s.quotaLoop(runCtx)
	go s.notifications.Start(runCtx)
	go s.backupLoop(runCtx)
	go s.runTelegramBot(runCtx)

	if s.cfg.Domain != "" {
		return s.runTLS(runCtx, handler)
	}

	httpServer := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe()
	}()
	s.logger.Info("control plane listening",
		"addr", s.cfg.Addr,
		"websocket", "/ws/agent",
		"public_url", s.cfg.PublicURL,
	)

	select {
	case err := <-errCh:
		s.hub.CloseAll()
		s.markAllNodesOffline()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		return s.shutdown(httpServer, nil)
	}
}

func (s *Server) runTLS(ctx context.Context, handler http.Handler) error {
	manager := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(s.cfg.Domain),
		Cache:      autocert.DirCache(s.cfg.CertDir),
		Email:      s.cfg.Email,
	}
	httpServer := &http.Server{
		Addr:              s.cfg.HTTPAddr,
		Handler:           manager.HTTPHandler(acmeRedirectHandler(s.cfg.Domain)),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	tlsConfig := manager.TLSConfig()
	tlsConfig.MinVersion = tls.VersionTLS12
	httpsServer := &http.Server{
		Addr:              s.cfg.HTTPSAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		TLSConfig:         tlsConfig,
	}

	errCh := make(chan error, 2)
	go func() { errCh <- httpServer.ListenAndServe() }()
	go func() { errCh <- httpsServer.ListenAndServeTLS("", "") }()
	s.logger.Info("control plane listening with ACME TLS",
		"domain", s.cfg.Domain,
		"http_addr", s.cfg.HTTPAddr,
		"https_addr", s.cfg.HTTPSAddr,
		"websocket", "/ws/agent",
		"public_url", s.cfg.PublicURL,
	)

	select {
	case err := <-errCh:
		s.hub.CloseAll()
		s.markAllNodesOffline()
		_ = httpServer.Close()
		_ = httpsServer.Close()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		return s.shutdown(httpServer, httpsServer)
	}
}

func acmeRedirectHandler(domain string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := "https://" + domain + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}

func (s *Server) shutdown(httpServer, httpsServer *http.Server) error {
	s.logger.Info("control plane shutting down")
	s.hub.CloseAll()
	s.markAllNodesOffline()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var firstErr error
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		firstErr = err
	}
	if httpsServer != nil {
		if err := httpsServer.Shutdown(shutdownCtx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Server) persistHeartbeats(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			views := s.hub.List()
			if len(views) == 0 {
				continue
			}
			ids := make([]string, 0, len(views))
			for _, view := range views {
				ids = append(ids, view.ID)
			}
			if err := s.store.TouchNodes(ids, now.UTC()); err != nil {
				s.logger.Error("persist node heartbeats", "error", err)
			}
		}
	}
}

func (s *Server) markAllNodesOffline() {
	if err := s.store.MarkAllNodesOffline(); err != nil {
		s.logger.Error("persist offline nodes", "error", err)
	}
}

func corsMiddleware(origins []string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			allowed[origin] = struct{}{}
		}
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if _, ok := allowed[origin]; origin != "" && ok {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
