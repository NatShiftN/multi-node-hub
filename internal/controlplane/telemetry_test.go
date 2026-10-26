package controlplane

import (
	"testing"
	"time"

	"nodehub/internal/protocol"
)

func TestTelemetryHubPublishesAndSnapshots(t *testing.T) {
	hub := newTelemetryHub()
	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	event := TelemetryEvent{
		NodeID:           "node-a",
		Timestamp:        time.Now().UTC(),
		CPUPercent:       12.5,
		MemoryPercent:    42,
		NetRXBytes:       1000,
		NetTXBytes:       500,
		MemoryTotalBytes: 100,
		MemoryUsedBytes:  50,
	}
	hub.Publish(event)
	select {
	case got := <-events:
		if got.NodeID != event.NodeID || got.CPUPercent != event.CPUPercent {
			t.Fatalf("unexpected event: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive telemetry")
	}
	snapshot := hub.Snapshot()
	if len(snapshot) != 1 || snapshot[0].NodeID != "node-a" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestTelemetrySanitizesSample(t *testing.T) {
	now := time.Now().UTC()
	event := telemetryEventFromSample("node-a", protocol.Telemetry{
		CPUPercent:       150,
		MemoryPercent:    -1,
		MemoryUsedBytes:  200,
		MemoryTotalBytes: 100,
	}, now)
	if event.CPUPercent != 100 || event.MemoryPercent != 0 {
		t.Fatalf("percentages not clamped: %+v", event)
	}
	if event.MemoryUsedBytes != 100 {
		t.Fatalf("memory used = %d, want 100", event.MemoryUsedBytes)
	}
}
