package plugins

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"nodehub/internal/protocol"
)

const (
	maxXrayConfigBytes  = 1 << 20
	xrayStartupGrace    = 800 * time.Millisecond
	xrayShutdownGrace   = 5 * time.Second
	xrayMaxLogBytes     = 10 << 20
	xrayRestartMaxDelay = 30 * time.Second
)

type XrayPluginConfig struct {
	DataDir       string
	BinaryPath    string
	DownloadURL   string
	StatsServer   string
	StatsInterval time.Duration
	Events        chan<- protocol.Envelope
	Logger        *slog.Logger
}

// XrayPlugin owns one Xray-core process. Apply of a new config stops the old
// process and starts a new one; a supervisor restarts unexpected exits.
type XrayPlugin struct {
	mu            sync.Mutex
	dataDir       string
	binaryPath    string
	configPath    string
	downloadURL   string
	statsServer   string
	statsInterval time.Duration
	events        chan<- protocol.Envelope
	logger        *slog.Logger
	runner        *xrayRunner
	statsCancel   context.CancelFunc
	statsDone     chan struct{}
	configDigest  [sha256.Size]byte
	hasConfig     bool
	closed        bool
}

func NewXrayPlugin(cfg XrayPluginConfig) (*XrayPlugin, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.DataDir == "" {
		cfg.DataDir = defaultXrayDataDir()
	}
	dataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve Xray data directory: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create Xray data directory: %w", err)
	}
	binaryName := "xray"
	if runtime.GOOS == "windows" {
		binaryName = "xray.exe"
	}
	binaryPath := cfg.BinaryPath
	if binaryPath == "" {
		binaryPath = filepath.Join(dataDir, binaryName)
	} else {
		binaryPath, err = filepath.Abs(binaryPath)
		if err != nil {
			return nil, fmt.Errorf("resolve Xray binary path: %w", err)
		}
	}
	if cfg.StatsServer == "" {
		cfg.StatsServer = "127.0.0.1:10085"
	}
	if cfg.StatsInterval <= 0 {
		cfg.StatsInterval = 30 * time.Second
	}
	return &XrayPlugin{
		dataDir:       dataDir,
		binaryPath:    binaryPath,
		configPath:    filepath.Join(dataDir, "config.json"),
		downloadURL:   cfg.DownloadURL,
		statsServer:   cfg.StatsServer,
		statsInterval: cfg.StatsInterval,
		events:        cfg.Events,
		logger:        cfg.Logger,
	}, nil
}

func (p *XrayPlugin) Name() string { return "xray" }

func (p *XrayPlugin) Execute(ctx context.Context, payload json.RawMessage) (any, error) {
	var desired protocol.XrayPayload
	if err := json.Unmarshal(payload, &desired); err != nil {
		return nil, fmt.Errorf("decode Xray payload: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("Xray plugin is closed")
	}
	if desired.Action == "status" {
		result := protocol.XrayApplyResult{
			Running:    p.runner != nil,
			BinaryPath: p.binaryPath,
			ConfigPath: p.configPath,
		}
		if p.runner != nil {
			result.PID = p.runner.pid()
		}
		return result, nil
	}

	if !desired.Enabled {
		p.stopStats()
		if p.runner != nil {
			p.runner.stop()
			p.runner = nil
		}
		p.hasConfig = false
		if err := os.Remove(p.configPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("remove Xray config: %w", err)
		}
		return protocol.XrayApplyResult{Running: false, BinaryPath: p.binaryPath}, nil
	}

	config, err := normalizeXrayConfig(desired.Config)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(p.dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create Xray data directory: %w", err)
	}
	digest := sha256.Sum256(config)
	if p.runner != nil && p.hasConfig && p.configDigest == digest {
		if p.statsCancel == nil {
			p.startStats()
		}
		return protocol.XrayApplyResult{
			Running:    true,
			PID:        p.runner.pid(),
			BinaryPath: p.binaryPath,
			ConfigPath: p.configPath,
		}, nil
	}
	downloaded, err := p.ensureBinary(ctx)
	if err != nil {
		return nil, err
	}
	if err := writeXrayConfig(p.configPath, config); err != nil {
		return nil, err
	}

	p.stopStats()
	if p.runner != nil {
		p.runner.stop()
		p.runner = nil
	}
	runner := newXrayRunner(p.binaryPath, p.configPath, p.dataDir, p.logger)
	if err := runner.start(ctx); err != nil {
		return nil, fmt.Errorf("start Xray: %w", err)
	}
	p.runner = runner
	p.configDigest = digest
	p.hasConfig = true
	p.startStats()
	return protocol.XrayApplyResult{
		Running:    true,
		PID:        runner.pid(),
		BinaryPath: p.binaryPath,
		ConfigPath: p.configPath,
		Downloaded: downloaded,
	}, nil
}

// Restore starts Xray from the last-known local config without contacting the
// control plane. This is what keeps the data plane alive after a Server outage
// or an Agent restart.
func (p *XrayPlugin) Restore(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.runner != nil {
		return nil
	}
	raw, err := os.ReadFile(p.configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if _, err := normalizeXrayConfig(raw); err != nil {
		return fmt.Errorf("restore local Xray config: %w", err)
	}
	if _, err := os.Stat(p.binaryPath); err != nil {
		p.logger.Warn("local Xray binary is missing; waiting for control plane", "path", p.binaryPath)
		return nil
	}
	p.stopStats()
	runner := newXrayRunner(p.binaryPath, p.configPath, p.dataDir, p.logger)
	if err := runner.start(ctx); err != nil {
		return fmt.Errorf("restore Xray process: %w", err)
	}
	p.runner = runner
	p.hasConfig = true
	p.startStats()
	p.logger.Info("Xray restored from local config", "config", p.configPath)
	return nil
}

func (p *XrayPlugin) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	runner := p.runner
	p.runner = nil
	p.hasConfig = false
	p.stopStats()
	p.mu.Unlock()
	if runner != nil {
		runner.stop()
	}
	return nil
}

func (p *XrayPlugin) startStats() {
	if p.events == nil || p.binaryPath == "" {
		return
	}
	raw := make([]byte, 8)
	if _, err := cryptorand.Read(raw); err != nil {
		p.logger.Warn("generate Xray stats session id", "error", err)
		return
	}
	sessionID := hex.EncodeToString(raw)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	p.statsCancel = cancel
	p.statsDone = done
	go p.trafficLoop(ctx, done, sessionID)
}

func (p *XrayPlugin) stopStats() {
	cancel := p.statsCancel
	done := p.statsDone
	p.statsCancel = nil
	p.statsDone = nil
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
}

func (p *XrayPlugin) trafficLoop(ctx context.Context, done chan<- struct{}, sessionID string) {
	defer close(done)
	delay := p.statsInterval
	if delay > 5*time.Second {
		delay = 5 * time.Second
	}
	if delay <= 0 {
		delay = time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			users, err := p.queryTraffic(ctx)
			if err != nil {
				// Stats API may be absent or Xray may still be starting. This is
				// best-effort and must never terminate the Agent.
				p.logger.Debug("query Xray traffic stats", "error", err)
			} else if len(users) > 0 {
				update := protocol.TrafficUpdate{
					SessionID: sessionID,
					Timestamp: time.Now().UTC(),
					Users:     users,
				}
				env := protocol.Envelope{
					Type:    protocol.TypeTrafficUpdate,
					Traffic: &update,
					SentAt:  time.Now().UTC(),
				}
				select {
				case p.events <- env:
				default:
					// Cumulative counters make a dropped frame self-healing.
					p.logger.Debug("drop traffic_update: event queue full")
				}
			}
			timer.Reset(p.statsInterval)
		}
	}
}

func (p *XrayPlugin) queryTraffic(ctx context.Context) ([]protocol.TrafficUserStat, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(
		queryCtx,
		p.binaryPath,
		"api",
		"statsquery",
		"--server="+p.statsServer,
		"-pattern",
		"user>>>",
	)
	cmd.Dir = p.dataDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, errors.New(message)
	}
	return parseXrayTrafficStats(output)
}

func parseXrayTrafficStats(output []byte) ([]protocol.TrafficUserStat, error) {
	output = bytes.TrimSpace(output)
	if len(output) == 0 {
		return nil, errors.New("empty stats response")
	}
	start := bytes.IndexByte(output, '{')
	end := bytes.LastIndexByte(output, '}')
	if start < 0 || end < start {
		return nil, errors.New("stats response is not JSON")
	}
	var response struct {
		Stat []struct {
			Name  string          `json:"name"`
			Value json.RawMessage `json:"value"`
		} `json:"stat"`
	}
	if err := json.Unmarshal(output[start:end+1], &response); err != nil {
		return nil, fmt.Errorf("decode stats response: %w", err)
	}

	users := make(map[string]*protocol.TrafficUserStat)
	for _, stat := range response.Stat {
		parts := strings.Split(stat.Name, ">>>")
		if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" {
			continue
		}
		email := strings.TrimSpace(parts[1])
		if email == "" {
			continue
		}
		value, err := parseCounterValue(stat.Value)
		if err != nil {
			continue
		}
		user := users[email]
		if user == nil {
			user = &protocol.TrafficUserStat{Email: email}
			users[email] = user
		}
		switch parts[3] {
		case "uplink":
			user.UploadBytes = value
		case "downlink":
			user.DownloadBytes = value
		}
	}
	result := make([]protocol.TrafficUserStat, 0, len(users))
	for _, user := range users {
		result = append(result, *user)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Email < result[j].Email })
	return result, nil
}

func parseCounterValue(raw json.RawMessage) (uint64, error) {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return 0, nil
	}
	if strings.HasPrefix(value, `"`) {
		var encoded string
		if err := json.Unmarshal([]byte(value), &encoded); err != nil {
			return 0, err
		}
		value = encoded
	}
	return strconv.ParseUint(value, 10, 64)
}

func (p *XrayPlugin) ensureBinary(ctx context.Context) (bool, error) {
	info, err := os.Stat(p.binaryPath)
	if err == nil {
		if info.IsDir() {
			return false, fmt.Errorf("Xray binary path is a directory: %s", p.binaryPath)
		}
		return false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("stat Xray binary: %w", err)
	}

	url, err := xrayDownloadURL(p.downloadURL)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(p.binaryPath), 0o750); err != nil {
		return false, fmt.Errorf("create Xray binary directory: %w", err)
	}
	p.logger.Info("downloading Xray-core", "url", url, "destination", p.binaryPath)
	if err := downloadXrayBinary(ctx, url, p.binaryPath); err != nil {
		return false, err
	}
	return true, nil
}

func normalizeXrayConfig(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, errors.New("xray config is required")
	}
	if len(raw) > maxXrayConfigBytes {
		return nil, fmt.Errorf("xray config exceeds %d bytes", maxXrayConfigBytes)
	}

	value := bytes.TrimSpace([]byte(raw))
	if len(value) > 0 && value[0] == '"' {
		var encoded string
		if err := json.Unmarshal(value, &encoded); err != nil {
			return nil, fmt.Errorf("decode Xray config string: %w", err)
		}
		value = bytes.TrimSpace([]byte(encoded))
	}
	if len(value) > maxXrayConfigBytes {
		return nil, fmt.Errorf("xray config exceeds %d bytes", maxXrayConfigBytes)
	}
	if len(value) == 0 || value[0] != '{' || !json.Valid(value) {
		return nil, errors.New("xray config must be a valid JSON object")
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, value); err != nil {
		return nil, fmt.Errorf("compact Xray config: %w", err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact.Bytes(), "", "  "); err != nil {
		return nil, fmt.Errorf("format Xray config: %w", err)
	}
	pretty.WriteByte('\n')
	return pretty.Bytes(), nil
}

func writeXrayConfig(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), "config-*.json")
	if err != nil {
		return fmt.Errorf("create Xray config temp file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("chmod Xray config: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write Xray config: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close Xray config: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(path)
		if retryErr := os.Rename(tempPath, path); retryErr != nil {
			return fmt.Errorf("install Xray config: %w", retryErr)
		}
	}
	return nil
}

func defaultXrayDataDir() string {
	if runtime.GOOS == "windows" {
		base := os.Getenv("ProgramData")
		if base == "" {
			base = `C:\ProgramData`
		}
		return filepath.Join(base, "NodeHub", "xray")
	}
	return "/var/lib/nodehub/xray"
}

type xrayRunner struct {
	binaryPath string
	configPath string
	dataDir    string
	logger     *slog.Logger

	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	stopOnce sync.Once

	cmdMu sync.Mutex
	cmd   *exec.Cmd
}

func newXrayRunner(binaryPath, configPath, dataDir string, logger *slog.Logger) *xrayRunner {
	ctx, cancel := context.WithCancel(context.Background())
	return &xrayRunner{
		binaryPath: binaryPath,
		configPath: configPath,
		dataDir:    dataDir,
		logger:     logger,
		ctx:        ctx,
		cancel:     cancel,
		done:       make(chan struct{}),
	}
}

func (r *xrayRunner) start(ctx context.Context) error {
	ready := make(chan error, 1)
	go r.run(ready)
	select {
	case err := <-ready:
		if err != nil {
			r.stop()
			return err
		}
		return nil
	case <-ctx.Done():
		r.stop()
		return ctx.Err()
	}
}

func (r *xrayRunner) run(ready chan<- error) {
	defer close(r.done)
	readySent := false
	backoff := time.Second
	for {
		if r.ctx.Err() != nil {
			return
		}

		logFile, err := openXrayLog(r.dataDir)
		if err != nil {
			if !readySent {
				ready <- fmt.Errorf("open Xray log: %w", err)
			}
			return
		}
		cmd := exec.Command(r.binaryPath, "run", "-c", r.configPath)
		configureXrayProcess(cmd)
		cmd.Dir = r.dataDir
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		if err := cmd.Start(); err != nil {
			_ = logFile.Close()
			if !readySent {
				ready <- fmt.Errorf("start Xray process: %w", err)
			}
			return
		}
		startedAt := time.Now()
		r.cmdMu.Lock()
		r.cmd = cmd
		r.cmdMu.Unlock()

		exitCh := make(chan error, 1)
		go func() { exitCh <- cmd.Wait() }()

		if !readySent {
			readySent = true
			timer := time.NewTimer(xrayStartupGrace)
			select {
			case err := <-exitCh:
				timer.Stop()
				_ = logFile.Close()
				r.clearCommand(cmd)
				ready <- fmt.Errorf("Xray exited during startup: %s", exitError(err))
				return
			case <-timer.C:
				ready <- nil
			case <-r.ctx.Done():
				timer.Stop()
				_ = terminateXrayProcess(cmd)
				<-exitCh
				_ = logFile.Close()
				r.clearCommand(cmd)
				return
			}
		}

		err = <-exitCh
		_ = logFile.Close()
		r.clearCommand(cmd)
		if r.ctx.Err() != nil {
			return
		}
		r.logger.Warn("Xray exited; supervisor will restart",
			"error", exitError(err),
			"backoff", backoff,
		)
		if time.Since(startedAt) > time.Minute {
			backoff = time.Second
		}
		timer := time.NewTimer(backoff)
		select {
		case <-r.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff *= 2
		if backoff > xrayRestartMaxDelay {
			backoff = xrayRestartMaxDelay
		}
	}
}

func (r *xrayRunner) stop() {
	r.stopOnce.Do(func() {
		r.cancel()
		r.cmdMu.Lock()
		cmd := r.cmd
		r.cmdMu.Unlock()
		if cmd != nil {
			_ = terminateXrayProcess(cmd)
		}
		select {
		case <-r.done:
			return
		case <-time.After(xrayShutdownGrace):
			if cmd != nil {
				killXrayProcess(cmd)
			}
		}
		select {
		case <-r.done:
		case <-time.After(2 * time.Second):
		}
	})
}

func (r *xrayRunner) pid() int {
	r.cmdMu.Lock()
	defer r.cmdMu.Unlock()
	if r.cmd == nil || r.cmd.Process == nil {
		return 0
	}
	return r.cmd.Process.Pid
}

func (r *xrayRunner) clearCommand(cmd *exec.Cmd) {
	r.cmdMu.Lock()
	if r.cmd == cmd {
		r.cmd = nil
	}
	r.cmdMu.Unlock()
}

func openXrayLog(dataDir string) (*os.File, error) {
	path := filepath.Join(dataDir, "xray.log")
	if info, err := os.Stat(path); err == nil && info.Size() > xrayMaxLogBytes {
		_ = os.Truncate(path, 0)
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

func exitError(err error) string {
	if err == nil {
		return "exit status 0"
	}
	return err.Error()
}

var _ io.Closer = (*XrayPlugin)(nil)
