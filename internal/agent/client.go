// Package agent implements the edge daemon: one outbound WebSocket, a plugin
// registry, and nothing that knows how plugins work internally.
package agent

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	mathrand "math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"nodehub/internal/plugins"
	"nodehub/internal/protocol"
	"nodehub/internal/telemetry"
)

const (
	writeWait             = 10 * time.Second
	pongWait              = 60 * time.Second
	pingPeriod            = 25 * time.Second
	handshakeTimeout      = 10 * time.Second
	maxConcurrentCommands = 8
	sendQueueSize         = 64
)

type Config struct {
	ServerURL      string
	Token          string
	ID             string
	Version        string
	CommandTimeout time.Duration
	ReconnectMin   time.Duration
	ReconnectMax   time.Duration
	Events         <-chan protocol.Envelope
}

func (c Config) Validate() error {
	u, err := url.Parse(c.ServerURL)
	if err != nil {
		return fmt.Errorf("parse server URL: %w", err)
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return errors.New("server URL must use ws:// or wss://")
	}
	if u.Host == "" {
		return errors.New("server URL has no host")
	}
	if c.Token == "" {
		return errors.New("agent token must not be empty")
	}
	if !protocol.ValidAgentID(c.ID) {
		return fmt.Errorf("invalid agent id %q (allowed: letters, digits, dot, underscore, dash; max 64)", c.ID)
	}
	if c.CommandTimeout <= 0 {
		return errors.New("command timeout must be positive")
	}
	if c.ReconnectMin <= 0 || c.ReconnectMax < c.ReconnectMin {
		return errors.New("invalid reconnect interval")
	}
	return nil
}

type Client struct {
	cfg       Config
	registry  *plugins.Registry
	logger    *slog.Logger
	dialer    *websocket.Dialer
	startedAt time.Time
	telemetry *telemetry.Collector
	events    <-chan protocol.Envelope
	updater   *Updater
}

func New(cfg Config, registry *plugins.Registry, logger *slog.Logger) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if registry == nil {
		return nil, errors.New("plugin registry is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		cfg:       cfg,
		registry:  registry,
		logger:    logger,
		startedAt: time.Now().UTC(),
		telemetry: telemetry.NewCollector(),
		events:    cfg.Events,
		updater:   NewUpdater(cfg.Version, logger),
		dialer: &websocket.Dialer{
			HandshakeTimeout: handshakeTimeout,
			Proxy:            http.ProxyFromEnvironment,
		},
	}, nil
}

// Run reconnects forever until ctx is cancelled.
func (c *Client) Run(ctx context.Context) error {
	backoff := c.cfg.ReconnectMin
	for {
		if ctx.Err() != nil {
			return nil
		}

		started := time.Now()
		err := c.connectAndServe(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if time.Since(started) > time.Minute {
			backoff = c.cfg.ReconnectMin
		}

		delay := jitter(backoff)
		c.logger.Warn("agent disconnected; reconnecting",
			"error", errString(err),
			"in", delay.Round(time.Millisecond),
		)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}

		backoff *= 2
		if backoff > c.cfg.ReconnectMax {
			backoff = c.cfg.ReconnectMax
		}
	}
}

func (c *Client) connectAndServe(ctx context.Context) error {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.cfg.Token)
	header.Set("X-Agent-ID", c.cfg.ID)
	header.Set("X-Agent-Version", c.cfg.Version)

	conn, resp, err := c.dialer.DialContext(ctx, c.cfg.ServerURL, header)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil {
			return fmt.Errorf("websocket dial (%s): %w", resp.Status, err)
		}
		return fmt.Errorf("websocket dial: %w", err)
	}
	defer conn.Close()

	conn.SetReadLimit(protocol.MaxMessageBytes)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	conn.SetPingHandler(func(data string) error {
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		err := conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(writeWait))
		if errors.Is(err, websocket.ErrCloseSent) {
			return nil
		}
		return err
	})

	hostname, _ := os.Hostname()
	hello := protocol.Envelope{
		Type: protocol.TypeHello,
		Agent: &protocol.AgentInfo{
			ID:        c.cfg.ID,
			Hostname:  hostname,
			OS:        runtime.GOOS,
			Arch:      runtime.GOARCH,
			Version:   c.cfg.Version,
			Plugins:   c.registry.Names(),
			StartedAt: c.startedAt,
		},
		SentAt: time.Now().UTC(),
	}
	_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
	if err := conn.WriteJSON(hello); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}

	c.logger.Info("connected to control plane",
		"server", c.cfg.ServerURL,
		"agent_id", c.cfg.ID,
		"plugins", c.registry.Names(),
	)
	return c.serve(ctx, conn)
}

func (c *Client) serve(parent context.Context, conn *websocket.Conn) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	send := make(chan []byte, sendQueueSize)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		defer conn.Close()
		c.writePump(ctx, conn, send)
	}()

	// A blocking ReadMessage cannot observe parent cancellation. Closing the
	// connection from this watcher makes shutdown immediate.
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-writerDone:
		}
	}()
	go c.telemetryLoop(ctx, send)
	go c.forwardEvents(ctx, send)

	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentCommands)
	defer func() {
		cancel()
		_ = conn.Close()
		<-writerDone
		wg.Wait()
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))

		var env protocol.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			c.logger.Warn("discard malformed message", "error", err)
			continue
		}

		switch env.Type {
		case protocol.TypeCommand:
			if env.RequestID == "" || env.Plugin == "" {
				c.logger.Warn("discard invalid command", "request_id", env.RequestID, "plugin", env.Plugin)
				continue
			}
			select {
			case sem <- struct{}{}:
				wg.Add(1)
				go func(env protocol.Envelope) {
					defer wg.Done()
					defer func() { <-sem }()
					c.executeAndReply(ctx, send, env)
				}(env)
			default:
				result := protocol.Result(env.RequestID, env.Plugin, nil, errors.New("agent is busy: too many concurrent commands"))
				c.sendResult(ctx, send, result)
			}
		case protocol.TypeVersionInfo:
			if env.Version != nil && c.updater != nil {
				c.updater.Offer(*env.Version)
			}
		case protocol.TypeHello:
			// Reserved for protocol negotiation in a later version.
		default:
			c.logger.Debug("ignore message type", "type", env.Type)
		}
	}
}

func (c *Client) forwardEvents(ctx context.Context, send chan<- []byte) {
	if c.events == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-c.events:
			raw, err := json.Marshal(event)
			if err != nil {
				c.logger.Debug("encode agent event", "type", event.Type, "error", err)
				continue
			}
			select {
			case send <- raw:
			default:
				c.logger.Debug("drop agent event: send queue full", "type", event.Type)
			}
		}
	}
}

func (c *Client) telemetryLoop(ctx context.Context, send chan<- []byte) {
	const interval = 5 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sample, err := c.telemetry.Collect(ctx)
			if err != nil {
				c.logger.Debug("collect telemetry", "error", err)
				continue
			}
			envelope := protocol.Envelope{
				Type:      protocol.TypeTelemetryUpdate,
				Telemetry: &sample,
				SentAt:    time.Now().UTC(),
			}
			raw, err := json.Marshal(envelope)
			if err != nil {
				c.logger.Debug("encode telemetry", "error", err)
				continue
			}
			select {
			case send <- raw:
			case <-ctx.Done():
				return
			default:
				// Telemetry is best-effort and must never block command results.
				c.logger.Debug("drop telemetry frame: send queue full")
			}
		}
	}
}

func (c *Client) executeAndReply(ctx context.Context, send chan<- []byte, env protocol.Envelope) {
	started := time.Now()
	timeout := c.cfg.CommandTimeout
	if env.TimeoutMS > 0 {
		requested := time.Duration(env.TimeoutMS) * time.Millisecond
		if requested > 10*time.Minute {
			requested = 10 * time.Minute
		}
		timeout = requested
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	data, err := executeSafely(execCtx, c.registry, env.Plugin, env.Payload)
	result := protocol.Result(env.RequestID, env.Plugin, data, err)
	result.DurationMS = time.Since(started).Milliseconds()

	logArgs := []any{
		"request_id", env.RequestID,
		"plugin", env.Plugin,
		"duration_ms", result.DurationMS,
	}
	if err != nil {
		logArgs = append(logArgs, "error", err)
		c.logger.Warn("plugin failed", logArgs...)
	} else {
		c.logger.Info("plugin completed", logArgs...)
	}
	c.sendResult(ctx, send, result)
}

func executeSafely(ctx context.Context, registry *plugins.Registry, name string, payload json.RawMessage) (data any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("plugin panic: %v", recovered)
		}
	}()
	return registry.Execute(ctx, name, payload)
}

func (c *Client) sendResult(ctx context.Context, send chan<- []byte, env *protocol.Envelope) {
	raw, err := json.Marshal(env)
	if err == nil && int64(len(raw)) > protocol.MaxMessageBytes {
		raw, err = json.Marshal(protocol.Result(
			env.RequestID,
			env.Plugin,
			nil,
			fmt.Errorf("plugin result is too large (%d bytes; max %d)", len(raw), protocol.MaxMessageBytes),
		))
	}
	if err != nil {
		c.logger.Error("encode command result", "error", err)
		return
	}
	select {
	case send <- raw:
	case <-ctx.Done():
	}
}

func (c *Client) writePump(ctx context.Context, conn *websocket.Conn, send <-chan []byte) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, "agent shutting down"),
				time.Now().Add(writeWait),
			)
			return
		case raw := <-send:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
				return
			}
		case <-ticker.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait)); err != nil {
				return
			}
		}
	}
}

// DefaultID derives a stable, URL-safe ID from the hostname. Use --id when
// running multiple agents on the same host.
func DefaultID() string {
	hostname, _ := os.Hostname()
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '_', r == '-':
			return r
		default:
			return '-'
		}
	}, hostname)
	cleaned = strings.Trim(cleaned, ".-_")
	if len(cleaned) > 64 {
		cleaned = cleaned[:64]
	}
	if cleaned == "" {
		buf := make([]byte, 4)
		if _, err := cryptorand.Read(buf); err == nil {
			cleaned = "agent-" + hex.EncodeToString(buf)
		} else {
			cleaned = fmt.Sprintf("agent-%d", time.Now().UnixNano())
		}
	}
	return cleaned
}

func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return time.Second
	}
	half := d / 2
	if half <= 0 {
		return d
	}
	return half + time.Duration(mathrand.Int64N(int64(half)+1))
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
