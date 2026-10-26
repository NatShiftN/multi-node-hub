// Package telemetry collects the small, fixed set of host metrics streamed to
// the control plane every five seconds.
package telemetry

import (
	"context"
	"fmt"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"

	"nodehub/internal/protocol"
)

type Collector struct{}

func NewCollector() *Collector { return &Collector{} }

func (c *Collector) Collect(ctx context.Context) (protocol.Telemetry, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	sample := protocol.Telemetry{Timestamp: time.Now().UTC()}

	percents, err := cpu.PercentWithContext(ctx, 0, false)
	if err != nil {
		return sample, fmt.Errorf("collect CPU: %w", err)
	}
	if len(percents) > 0 {
		sample.CPUPercent = clampPercent(percents[0])
	}

	vm, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return sample, fmt.Errorf("collect memory: %w", err)
	}
	sample.MemoryPercent = clampPercent(vm.UsedPercent)
	sample.MemoryUsedBytes = vm.Used
	sample.MemoryTotalBytes = vm.Total

	counters, err := gnet.IOCountersWithContext(ctx, false)
	if err != nil {
		return sample, fmt.Errorf("collect network: %w", err)
	}
	if len(counters) > 0 {
		sample.NetRXBytes = counters[0].BytesRecv
		sample.NetTXBytes = counters[0].BytesSent
	}
	return sample, nil
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
