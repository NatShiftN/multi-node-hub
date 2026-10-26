package plugins

import (
	"context"
	"encoding/json"
	"log/slog"
	"os/exec"
	"time"
)

type RebootPlugin struct {
	logger *slog.Logger
}

func NewRebootPlugin(logger *slog.Logger) *RebootPlugin {
	if logger == nil {
		logger = slog.Default()
	}
	return &RebootPlugin{logger: logger}
}

func (p *RebootPlugin) Name() string { return "reboot" }

func (p *RebootPlugin) Execute(context.Context, json.RawMessage) (any, error) {
	go func() {
		time.Sleep(500 * time.Millisecond)
		if err := exec.Command("systemctl", "reboot").Run(); err != nil {
			p.logger.Error("system reboot failed", "error", err)
		}
	}()
	return map[string]any{"accepted": true}, nil
}
