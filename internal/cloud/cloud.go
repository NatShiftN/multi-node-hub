// Package cloud adapts control-plane node replacement to cloud providers.
package cloud

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Provider replaces a node's public address. Implementations must be
// idempotent enough for an operator retry after a transient API failure.
type Provider interface {
	Name() string
	Validate(ctx context.Context) error
	ReplaceIP(ctx context.Context, instanceID string) (newIP string, err error)
	PowerOff(ctx context.Context, instanceID string) error
	PowerOn(ctx context.Context, instanceID string) error
	Reboot(ctx context.Context, instanceID string) error
	GetBandwidth(ctx context.Context, instanceID string) (Bandwidth, error)
}

type Bandwidth struct {
	UsedBytes      uint64 `json:"used_bytes"`
	AllocatedBytes uint64 `json:"allocated_bytes"`
	Period         string `json:"period"`
}

// InstanceReplacer is optionally implemented by providers that rebuild a
// machine and return a new provider instance ID (Vultr destroy/recreate).
type InstanceReplacer interface {
	ReplaceIPWithInstance(ctx context.Context, instanceID string) (newIP, newInstanceID string, err error)
}

type Registry struct {
	providers map[string]Provider
}

func NewRegistry(providers ...Provider) *Registry {
	registry := &Registry{providers: make(map[string]Provider, len(providers))}
	for _, provider := range providers {
		if provider != nil {
			registry.providers[provider.Name()] = provider
		}
	}
	return registry
}

func (r *Registry) Get(name string) (Provider, bool) {
	provider, ok := r.providers[strings.ToLower(strings.TrimSpace(name))]
	return provider, ok
}

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}

// MockProvider makes the whole replace-IP -> DDNS flow testable without keys.
type MockProvider struct {
	Logger *slog.Logger
}

func (m *MockProvider) Name() string { return "mock" }

func (m *MockProvider) Validate(ctx context.Context) error { return nil }

func (m *MockProvider) ReplaceIP(ctx context.Context, instanceID string) (string, error) {
	if instanceID == "" {
		return "", errors.New("instance id is required")
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(150 * time.Millisecond):
	}
	var octet [1]byte
	if _, err := rand.Read(octet[:]); err != nil {
		return "", err
	}
	ip := fmt.Sprintf("198.51.100.%d", int(octet[0])%254+1)
	if m.Logger != nil {
		m.Logger.Info("mock cloud IP replacement", "instance_id", instanceID, "new_ip", ip)
	}
	return ip, nil
}

func (m *MockProvider) PowerOff(ctx context.Context, instanceID string) error {
	return m.powerAction(ctx, instanceID, "power_off")
}

func (m *MockProvider) PowerOn(ctx context.Context, instanceID string) error {
	return m.powerAction(ctx, instanceID, "power_on")
}

func (m *MockProvider) Reboot(ctx context.Context, instanceID string) error {
	return m.powerAction(ctx, instanceID, "reboot")
}

func (m *MockProvider) powerAction(ctx context.Context, instanceID, action string) error {
	if instanceID == "" {
		return errors.New("instance id is required")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(100 * time.Millisecond):
	}
	if m.Logger != nil {
		m.Logger.Info("mock cloud power action", "instance_id", instanceID, "action", action)
	}
	return nil
}

func (m *MockProvider) GetBandwidth(ctx context.Context, instanceID string) (Bandwidth, error) {
	if instanceID == "" {
		return Bandwidth{}, errors.New("instance id is required")
	}
	select {
	case <-ctx.Done():
		return Bandwidth{}, ctx.Err()
	case <-time.After(50 * time.Millisecond):
	}
	return Bandwidth{UsedBytes: 5 << 30, AllocatedBytes: 1 << 40, Period: "month"}, nil
}

func newHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}
