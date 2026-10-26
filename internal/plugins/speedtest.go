package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"nodehub/internal/protocol"
)

const (
	defaultSpeedtestDownload = "https://speed.cloudflare.com/__down?bytes=25000000"
	defaultSpeedtestUpload   = "https://speed.cloudflare.com/__up"
	speedtestUploadBytes     = 8 << 20
)

type SpeedtestPluginConfig struct {
	DownloadURL string
	UploadURL   string
	PingAddr    string
	Logger      *slog.Logger
}

type SpeedtestPlugin struct {
	downloadURL string
	uploadURL   string
	pingAddr    string
	client      *http.Client
	logger      *slog.Logger
}

func NewSpeedtestPlugin(cfg SpeedtestPluginConfig) (*SpeedtestPlugin, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.DownloadURL == "" {
		cfg.DownloadURL = defaultSpeedtestDownload
	}
	if cfg.UploadURL == "" {
		cfg.UploadURL = defaultSpeedtestUpload
	}
	download, err := url.Parse(cfg.DownloadURL)
	if err != nil || download.Host == "" || (download.Scheme != "http" && download.Scheme != "https") {
		return nil, errors.New("speedtest download URL must be http(s)")
	}
	upload, err := url.Parse(cfg.UploadURL)
	if err != nil || upload.Host == "" || (upload.Scheme != "http" && upload.Scheme != "https") {
		return nil, errors.New("speedtest upload URL must be http(s)")
	}
	if cfg.PingAddr == "" {
		port := download.Port()
		if port == "" {
			port = "443"
			if download.Scheme == "http" {
				port = "80"
			}
		}
		cfg.PingAddr = net.JoinHostPort(download.Hostname(), port)
	}
	if _, _, err := net.SplitHostPort(cfg.PingAddr); err != nil {
		return nil, fmt.Errorf("speedtest ping address must be host:port: %w", err)
	}
	plugin := &SpeedtestPlugin{
		downloadURL: cfg.DownloadURL,
		uploadURL:   cfg.UploadURL,
		pingAddr:    cfg.PingAddr,
		logger:      cfg.Logger,
		client: &http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: 6 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				TLSHandshakeTimeout:   6 * time.Second,
				ResponseHeaderTimeout: 15 * time.Second,
				DisableCompression:    true,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          4,
				IdleConnTimeout:       30 * time.Second,
			},
		},
	}
	return plugin, nil
}

func (p *SpeedtestPlugin) Name() string { return "speedtest" }

func (p *SpeedtestPlugin) Execute(ctx context.Context, _ json.RawMessage) (any, error) {
	started := time.Now()
	pingMS, err := p.measurePing(ctx, 3)
	if err != nil {
		return nil, fmt.Errorf("speedtest ping: %w", err)
	}
	downloadBytes, downloadMbps, err := p.measureDownload(ctx)
	if err != nil {
		return nil, fmt.Errorf("speedtest download: %w", err)
	}
	uploadBytes, uploadMbps, err := p.measureUpload(ctx)
	if err != nil {
		return nil, fmt.Errorf("speedtest upload: %w", err)
	}
	server := p.downloadURL
	if parsed, err := url.Parse(p.downloadURL); err == nil {
		server = parsed.Host
	}
	return protocol.SpeedtestResult{
		PingMS:        round2(pingMS),
		DownloadMbps:  round2(downloadMbps),
		UploadMbps:    round2(uploadMbps),
		DownloadBytes: downloadBytes,
		UploadBytes:   uploadBytes,
		Server:        server,
		Timestamp:     time.Now().UTC(),
		DurationMS:    time.Since(started).Milliseconds(),
	}, nil
}

func (p *SpeedtestPlugin) measurePing(ctx context.Context, attempts int) (float64, error) {
	dialer := &net.Dialer{Timeout: 6 * time.Second}
	var total time.Duration
	successes := 0
	for i := 0; i < attempts; i++ {
		start := time.Now()
		conn, err := dialer.DialContext(ctx, "tcp", p.pingAddr)
		if err != nil {
			continue
		}
		total += time.Since(start)
		successes++
		_ = conn.Close()
	}
	if successes == 0 {
		return 0, errors.New("all TCP handshakes failed")
	}
	return float64(total.Microseconds()) / float64(successes) / 1000, nil
}

func (p *SpeedtestPlugin) measureDownload(ctx context.Context) (uint64, float64, error) {
	opCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(opCtx, http.MethodGet, p.downloadURL, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Cache-Control", "no-store")
	start := time.Now()
	response, err := p.client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, 0, fmt.Errorf("download HTTP %d", response.StatusCode)
	}
	written, readErr := io.Copy(io.Discard, response.Body)
	elapsed := time.Since(start)
	if written == 0 {
		if readErr != nil {
			return 0, 0, readErr
		}
		return 0, 0, errors.New("download returned no bytes")
	}
	if readErr != nil && !errors.Is(opCtx.Err(), context.DeadlineExceeded) {
		return 0, 0, readErr
	}
	return uint64(written), calculateMbps(uint64(written), elapsed), nil
}

func (p *SpeedtestPlugin) measureUpload(ctx context.Context) (uint64, float64, error) {
	opCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	counter := &countingReader{reader: io.LimitReader(zeroReader{}, speedtestUploadBytes)}
	req, err := http.NewRequestWithContext(opCtx, http.MethodPost, p.uploadURL, counter)
	if err != nil {
		return 0, 0, err
	}
	req.ContentLength = speedtestUploadBytes
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Cache-Control", "no-store")
	start := time.Now()
	response, err := p.client.Do(req)
	if err != nil {
		return uint64(counter.count), 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	elapsed := time.Since(start)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return uint64(counter.count), 0, fmt.Errorf("upload HTTP %d", response.StatusCode)
	}
	if counter.count == 0 {
		return 0, 0, errors.New("upload sent no bytes")
	}
	return uint64(counter.count), calculateMbps(uint64(counter.count), elapsed), nil
}

func calculateMbps(bytes uint64, elapsed time.Duration) float64 {
	if bytes == 0 || elapsed <= 0 {
		return 0
	}
	return float64(bytes) * 8 / 1_000_000 / elapsed.Seconds()
}

type countingReader struct {
	reader io.Reader
	count  int64
}

func (c *countingReader) Read(buffer []byte) (int, error) {
	n, err := c.reader.Read(buffer)
	c.count += int64(n)
	return n, err
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = 0
	}
	return len(buffer), nil
}
