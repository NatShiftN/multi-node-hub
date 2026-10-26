// Package plugins contains the edge-side plugin contract and built-in plugins.
package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Plugin is the only extension point in the agent. One plugin may expose many
// actions by interpreting its JSON payload; the MVP keeps one action per plugin.
type Plugin interface {
	Name() string
	Execute(ctx context.Context, payload json.RawMessage) (any, error)
}

// ErrNotFound is returned when the control plane asks for an unregistered plugin.
var ErrNotFound = errors.New("plugin not found")

// Registry is a concurrency-safe plugin lookup table.
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]Plugin
}

func NewRegistry(items ...Plugin) (*Registry, error) {
	r := &Registry{plugins: make(map[string]Plugin, len(items))}
	for _, p := range items {
		if err := r.Register(p); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Registry) Register(p Plugin) error {
	if p == nil {
		return errors.New("register nil plugin")
	}
	name := p.Name()
	if name == "" {
		return errors.New("plugin name must not be empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.plugins[name]; exists {
		return fmt.Errorf("plugin %q already registered", name)
	}
	r.plugins[name] = p
	return nil
}

func (r *Registry) Execute(ctx context.Context, name string, payload json.RawMessage) (any, error) {
	r.mu.RLock()
	p, ok := r.plugins[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	return p.Execute(ctx, payload)
}

// Names returns a sorted snapshot for the agent hello message.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.plugins))
	for name := range r.plugins {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
