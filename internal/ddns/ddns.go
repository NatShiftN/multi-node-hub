// Package ddns updates a node's public DNS record after an IP change.
package ddns

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

type Request struct {
	ZoneID   string
	RecordID string
	Domain   string
	IP       string
}

type Provider interface {
	Name() string
	Update(ctx context.Context, request Request) error
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

type MockProvider struct {
	Logger *slog.Logger

	mu       sync.Mutex
	Last     Request
	Attempts int
}

func (m *MockProvider) Name() string { return "mock" }

func (m *MockProvider) Update(ctx context.Context, request Request) error {
	if request.ZoneID == "" || request.Domain == "" || net.ParseIP(request.IP) == nil {
		return errors.New("invalid DDNS request")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(100 * time.Millisecond):
	}
	m.mu.Lock()
	m.Last = request
	m.Attempts++
	m.mu.Unlock()
	if m.Logger != nil {
		m.Logger.Info("mock DDNS update", "domain", request.Domain, "ip", request.IP)
	}
	return nil
}

func (m *MockProvider) LastRequest() (Request, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Last, m.Attempts
}
