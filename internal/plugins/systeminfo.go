package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

// SystemInfo is the reference plugin. It demonstrates CPU/RAM collection while
// remaining a normal registry entry: the core knows nothing about it.
type SystemInfo struct{}

type SystemInfoResult struct {
	Hostname      string    `json:"hostname"`
	OS            string    `json:"os"`
	Platform      string    `json:"platform,omitempty"`
	Arch          string    `json:"arch"`
	CPUModel      string    `json:"cpu_model,omitempty"`
	CPUCores      int       `json:"cpu_cores"`
	CPUPercent    float64   `json:"cpu_percent"`
	MemoryTotal   uint64    `json:"memory_total_bytes"`
	MemoryUsed    uint64    `json:"memory_used_bytes"`
	MemoryPercent float64   `json:"memory_percent"`
	UptimeSeconds uint64    `json:"uptime_seconds"`
	CollectedAt   time.Time `json:"collected_at"`
	Warnings      []string  `json:"warnings,omitempty"`
}

func (SystemInfo) Name() string { return "system_info" }

func (SystemInfo) Execute(ctx context.Context, _ json.RawMessage) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := SystemInfoResult{
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		CPUCores:    runtime.NumCPU(),
		CollectedAt: time.Now().UTC(),
	}

	if hostname, err := os.Hostname(); err == nil {
		result.Hostname = hostname
	}

	// cpu.Percent needs two samples. 250ms is short enough for a UI call and
	// long enough to produce a useful value.
	percents, err := cpu.PercentWithContext(ctx, 250*time.Millisecond, false)
	if err != nil {
		result.Warnings = append(result.Warnings, "cpu sample: "+err.Error())
	} else if len(percents) > 0 {
		result.CPUPercent = round2(percents[0])
	}

	if infos, err := cpu.InfoWithContext(ctx); err == nil && len(infos) > 0 {
		result.CPUModel = infos[0].ModelName
	}

	vm, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("read virtual memory: %w", err)
	}
	result.MemoryTotal = vm.Total
	result.MemoryUsed = vm.Used
	result.MemoryPercent = round2(vm.UsedPercent)

	if hi, err := host.InfoWithContext(ctx); err == nil {
		if hi.Hostname != "" {
			result.Hostname = hi.Hostname
		}
		result.Platform = joinPlatform(hi.Platform, hi.PlatformVersion)
		result.UptimeSeconds = hi.Uptime
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

func joinPlatform(name, version string) string {
	switch {
	case name == "":
		return version
	case version == "":
		return name
	default:
		return name + " " + version
	}
}
