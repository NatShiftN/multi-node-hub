package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

type DigitalOceanProvider struct {
	Token      string
	BaseURL    string
	HTTPClient *http.Client
	Logger     *slog.Logger
}

func NewDigitalOceanProvider(token, baseURL string, logger *slog.Logger) *DigitalOceanProvider {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.digitalocean.com/v2"
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &DigitalOceanProvider{
		Token:      strings.TrimSpace(token),
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTPClient: newHTTPClient(),
		Logger:     logger,
	}
}

func (d *DigitalOceanProvider) Name() string { return "digitalocean" }

func (d *DigitalOceanProvider) Validate(ctx context.Context) error {
	if d.Token == "" {
		return errors.New("DIGITALOCEAN_TOKEN is not configured")
	}
	return d.request(ctx, http.MethodGet, "/account", nil, nil)
}

func (d *DigitalOceanProvider) ReplaceIP(context.Context, string) (string, error) {
	return "", errors.New("DigitalOcean ReplaceIP is not supported; use power actions or a reserved IP workflow")
}

func (d *DigitalOceanProvider) PowerOff(ctx context.Context, instanceID string) error {
	return d.action(ctx, instanceID, "power_off")
}

func (d *DigitalOceanProvider) PowerOn(ctx context.Context, instanceID string) error {
	return d.action(ctx, instanceID, "power_on")
}

func (d *DigitalOceanProvider) Reboot(ctx context.Context, instanceID string) error {
	return d.action(ctx, instanceID, "reboot")
}

func (d *DigitalOceanProvider) action(ctx context.Context, instanceID, action string) error {
	if d.Token == "" {
		return errors.New("DIGITALOCEAN_TOKEN is not configured")
	}
	if instanceID == "" {
		return errors.New("droplet id is required")
	}
	body := map[string]any{"type": action}
	return d.request(ctx, http.MethodPost, "/droplets/"+url.PathEscape(instanceID)+"/actions", body, nil)
}

func (d *DigitalOceanProvider) GetBandwidth(context.Context, string) (Bandwidth, error) {
	// DigitalOcean does not expose per-droplet bandwidth through the v2 API.
	return Bandwidth{Period: "month"}, nil
}

func (d *DigitalOceanProvider) request(ctx context.Context, method, path string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, d.BaseURL+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+d.Token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := d.HTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("DigitalOcean HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(raw)))
	}
	if target != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, target); err != nil {
			return err
		}
	}
	return nil
}

var _ Provider = (*DigitalOceanProvider)(nil)
