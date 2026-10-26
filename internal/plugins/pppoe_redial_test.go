package plugins

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func testPluginLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPPPoERedialRunsValidatedCommandsAndReportsIP(t *testing.T) {
	plugin, err := NewPPPoERedialPlugin(PPPoERedialConfig{
		Interface: "ppp0",
		Provider:  "dsl-provider",
		Logger:    testPluginLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	plugin.runCommand = func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	ips := []string{"203.0.113.10", "203.0.113.11"}
	last := ""
	plugin.lookupIP = func(context.Context) (string, error) {
		if len(ips) == 0 {
			return last, nil
		}
		last = ips[0]
		ips = ips[1:]
		return last, nil
	}
	plugin.pollInterval = time.Millisecond
	plugin.waitTimeout = 200 * time.Millisecond

	value, err := plugin.Execute(context.Background(), []byte(`{"reason":"gfw"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	result, ok := value.(PPPoERedialResult)
	if !ok || !result.Redialed || result.NewIP != "203.0.113.11" {
		t.Fatalf("result = %+v", value)
	}
	joined := strings.Join(calls, "\n")
	for _, expected := range []string{
		"ip link set dev ppp0 down",
		"ip link set dev ppp0 up",
		"poff -a",
		"pon dsl-provider",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing command %q in:\n%s", expected, joined)
		}
	}
}

func TestPPPoERedialRejectsUnsafeNames(t *testing.T) {
	if _, err := NewPPPoERedialPlugin(PPPoERedialConfig{Interface: "ppp0; rm -rf /", Logger: testPluginLogger()}); err == nil {
		t.Fatal("expected invalid interface error")
	}
	if _, err := NewPPPoERedialPlugin(PPPoERedialConfig{Provider: "dsl-provider && id", Logger: testPluginLogger()}); err == nil {
		t.Fatal("expected invalid provider error")
	}
}
