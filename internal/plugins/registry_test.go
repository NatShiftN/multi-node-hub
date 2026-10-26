package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

type fakePlugin struct {
	name    string
	payload json.RawMessage
}

func (p *fakePlugin) Name() string { return p.name }

func (p *fakePlugin) Execute(_ context.Context, payload json.RawMessage) (any, error) {
	p.payload = payload
	return map[string]string{"plugin": p.name}, nil
}

func TestRegistryExecuteAndNames(t *testing.T) {
	first := &fakePlugin{name: "alpha"}
	second := &fakePlugin{name: "beta"}
	registry, err := NewRegistry(first, second)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if got, want := registry.Names(), []string{"alpha", "beta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}

	result, err := registry.Execute(context.Background(), "alpha", nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := result.(map[string]string)["plugin"]; got != "alpha" {
		t.Fatalf("result plugin = %q", got)
	}
	if string(first.payload) != "{}" {
		t.Fatalf("payload = %s, want default {}", first.payload)
	}
}

func TestRegistryUnknownPlugin(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	_, err = registry.Execute(context.Background(), "missing", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestRegistryRejectsDuplicateAndEmptyPlugins(t *testing.T) {
	registry, err := NewRegistry(&fakePlugin{name: "same"})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if err := registry.Register(&fakePlugin{name: "same"}); err == nil {
		t.Fatal("duplicate registration should fail")
	}
	if err := registry.Register(nil); err == nil {
		t.Fatal("nil registration should fail")
	}
	if err := registry.Register(&fakePlugin{name: ""}); err == nil {
		t.Fatal("empty name registration should fail")
	}
}
