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
	"time"
)

type VultrProvider struct {
	APIKey       string
	BaseURL      string
	HTTPClient   *http.Client
	PollInterval time.Duration
	Logger       *slog.Logger
	OnOrphan     func(instanceID string, err error)
}

type vultrInstance struct {
	ID               string  `json:"id"`
	Region           string  `json:"region"`
	Plan             string  `json:"plan"`
	OSID             int     `json:"os_id"`
	Label            string  `json:"label"`
	Hostname         string  `json:"hostname"`
	MainIP           string  `json:"main_ip"`
	EnableIPv6       bool    `json:"enable_ipv6"`
	Backups          string  `json:"backups"`
	DDOSProtection   bool    `json:"ddos_protection"`
	UserData         string  `json:"user_data"`
	AllowedBandwidth int64   `json:"allowed_bandwidth"`
	CurrentBandwidth float64 `json:"current_bandwidth"`
}

func NewVultrProvider(apiKey, baseURL string, logger *slog.Logger) *VultrProvider {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.vultr.com/v2"
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &VultrProvider{
		APIKey:       strings.TrimSpace(apiKey),
		BaseURL:      strings.TrimRight(baseURL, "/"),
		HTTPClient:   newHTTPClient(),
		PollInterval: 5 * time.Second,
		Logger:       logger,
	}
}

func (v *VultrProvider) Name() string { return "vultr" }

func (v *VultrProvider) Validate(ctx context.Context) error {
	if v.APIKey == "" {
		return errors.New("VULTR_API_KEY is not configured")
	}
	return v.request(ctx, http.MethodGet, "/account", nil, nil)
}

func (v *VultrProvider) ReplaceIP(ctx context.Context, instanceID string) (string, error) {
	ip, _, err := v.ReplaceIPWithInstance(ctx, instanceID)
	return ip, err
}

// ReplaceIPWithInstance performs a create-first rebuild. Vultr v2 has no
// in-place public IPv4 swap. The old instance is deleted only after the
// replacement is running and has a public IPv4. A create failure or IP wait
// timeout leaves the old instance intact; a failed delete after success is
// logged as an orphan for manual cleanup rather than causing an outage.
func (v *VultrProvider) ReplaceIPWithInstance(ctx context.Context, instanceID string) (string, string, error) {
	if v.APIKey == "" {
		return "", "", errors.New("VULTR_API_KEY is not configured")
	}
	if instanceID == "" {
		return "", "", errors.New("instance id is required")
	}
	old, err := v.instance(ctx, instanceID)
	if err != nil {
		return "", "", err
	}
	if old.Region == "" || old.Plan == "" || old.OSID == 0 {
		return "", "", errors.New("Vultr instance is missing region/plan/os_id")
	}
	body := map[string]any{
		"region":          old.Region,
		"plan":            old.Plan,
		"os_id":           old.OSID,
		"label":           old.Label,
		"hostname":        old.Hostname,
		"enable_ipv6":     old.EnableIPv6,
		"backups":         old.Backups,
		"ddos_protection": old.DDOSProtection,
	}
	if old.UserData != "" {
		body["user_data"] = old.UserData
	}
	var created struct {
		Instance vultrInstance `json:"instance"`
	}
	if err := v.request(ctx, http.MethodPost, "/instances", body, &created); err != nil {
		// Old instance is still running: create failed before any destructive step.
		return "", "", fmt.Errorf("create replacement Vultr instance (old instance preserved): %w", err)
	}
	if created.Instance.ID == "" {
		return "", "", errors.New("Vultr create response is missing instance id (old instance preserved)")
	}

	cleanupNew := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := v.request(cleanupCtx, http.MethodDelete, "/instances/"+url.PathEscape(created.Instance.ID), nil, nil); err != nil && v.Logger != nil {
			v.Logger.Error("failed to roll back replacement instance",
				"new_instance_id", created.Instance.ID, "error", err)
		}
	}

	interval := v.PollInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		current, err := v.instance(ctx, created.Instance.ID)
		if err == nil && current.MainIP != "" && current.MainIP != "0.0.0.0" {
			// Replacement is healthy. Only now is the old instance removed.
			if err := v.request(ctx, http.MethodDelete, "/instances/"+url.PathEscape(instanceID), nil, nil); err != nil {
				if v.OnOrphan != nil {
					v.OnOrphan(instanceID, err)
				}
				if v.Logger != nil {
					v.Logger.Error("replacement ready but old Vultr instance delete failed; orphan needs manual cleanup",
						"old_instance_id", instanceID,
						"new_instance_id", created.Instance.ID,
						"new_ip", current.MainIP,
						"error", err,
					)
				}
			}
			if v.Logger != nil {
				v.Logger.Warn("Vultr instance rebuilt; provider instance id changed",
					"old_instance_id", instanceID,
					"new_instance_id", created.Instance.ID,
					"new_ip", current.MainIP,
				)
			}
			return current.MainIP, created.Instance.ID, nil
		}
		select {
		case <-ctx.Done():
			cleanupNew()
			return "", "", fmt.Errorf("wait for replacement IP: %w (old instance preserved, replacement rolled back)", ctx.Err())
		case <-ticker.C:
		}
	}
}

// DeleteInstance is used by the control plane's orphan retry worker.
func (v *VultrProvider) DeleteInstance(ctx context.Context, instanceID string) error {
	if v.APIKey == "" {
		return errors.New("VULTR_API_KEY is not configured")
	}
	if instanceID == "" {
		return errors.New("instance id is required")
	}
	return v.request(ctx, http.MethodDelete, "/instances/"+url.PathEscape(instanceID), nil, nil)
}

func (v *VultrProvider) PowerOff(ctx context.Context, instanceID string) error {
	return v.power(ctx, instanceID, "halt")
}

func (v *VultrProvider) PowerOn(ctx context.Context, instanceID string) error {
	return v.power(ctx, instanceID, "start")
}

func (v *VultrProvider) Reboot(ctx context.Context, instanceID string) error {
	return v.power(ctx, instanceID, "reboot")
}

func (v *VultrProvider) power(ctx context.Context, instanceID, action string) error {
	if v.APIKey == "" {
		return errors.New("VULTR_API_KEY is not configured")
	}
	if instanceID == "" {
		return errors.New("instance id is required")
	}
	if err := v.request(ctx, http.MethodPost, "/instances/"+url.PathEscape(instanceID)+"/"+action, nil, nil); err != nil {
		return fmt.Errorf("Vultr %s: %w", action, err)
	}
	return nil
}

func (v *VultrProvider) GetBandwidth(ctx context.Context, instanceID string) (Bandwidth, error) {
	instance, err := v.instance(ctx, instanceID)
	if err != nil {
		return Bandwidth{}, err
	}
	return Bandwidth{
		UsedBytes:      uint64(instance.CurrentBandwidth * 1e9),
		AllocatedBytes: uint64(instance.AllowedBandwidth) * 1e9,
		Period:         "month",
	}, nil
}

func (v *VultrProvider) instance(ctx context.Context, instanceID string) (vultrInstance, error) {
	var response struct {
		Instance vultrInstance `json:"instance"`
	}
	if err := v.request(ctx, http.MethodGet, "/instances/"+url.PathEscape(instanceID), nil, &response); err != nil {
		return vultrInstance{}, err
	}
	return response.Instance, nil
}

func (v *VultrProvider) request(ctx context.Context, method, path string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, v.BaseURL+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+v.APIKey)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := v.HTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(raw))
		if len(message) > 512 {
			message = message[:512]
		}
		return fmt.Errorf("Vultr API HTTP %d: %s", response.StatusCode, message)
	}
	if target == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, target)
}
