package controlplane

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/protocol"
)

type TelemetryEvent struct {
	NodeID           string    `json:"node_id"`
	Timestamp        time.Time `json:"timestamp"`
	CPUPercent       float64   `json:"cpu_percent"`
	MemoryPercent    float64   `json:"memory_percent"`
	MemoryUsedBytes  uint64    `json:"memory_used_bytes"`
	MemoryTotalBytes uint64    `json:"memory_total_bytes"`
	NetRXBytes       uint64    `json:"net_rx_bytes"`
	NetTXBytes       uint64    `json:"net_tx_bytes"`
}

type telemetryHub struct {
	mu          sync.RWMutex
	latest      map[string]TelemetryEvent
	history     map[string][]float64
	subscribers map[chan TelemetryEvent]struct{}
}

func newTelemetryHub() *telemetryHub {
	return &telemetryHub{
		latest:      make(map[string]TelemetryEvent),
		history:     make(map[string][]float64),
		subscribers: make(map[chan TelemetryEvent]struct{}),
	}
}

func (h *telemetryHub) Publish(event TelemetryEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.latest[event.NodeID] = event
	h.history[event.NodeID] = append(h.history[event.NodeID], event.CPUPercent)
	if len(h.history[event.NodeID]) > 30 {
		h.history[event.NodeID] = h.history[event.NodeID][len(h.history[event.NodeID])-30:]
	}
	for subscriber := range h.subscribers {
		select {
		case subscriber <- event:
		default:
			// A slow dashboard must not backpressure Agents.
		}
	}
}

func (h *telemetryHub) Subscribe() (<-chan TelemetryEvent, func()) {
	ch := make(chan TelemetryEvent, 64)
	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()

	unsubscribe := func() {
		h.mu.Lock()
		if _, ok := h.subscribers[ch]; ok {
			delete(h.subscribers, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
	return ch, unsubscribe
}

func (h *telemetryHub) Get(nodeID string) (TelemetryEvent, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	event, ok := h.latest[nodeID]
	return event, ok
}

func (h *telemetryHub) History(nodeID string) []float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	source := h.history[nodeID]
	result := make([]float64, len(source))
	copy(result, source)
	return result
}

func (h *telemetryHub) AverageLoad() (cpu, memory float64, count int) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, event := range h.latest {
		cpu += event.CPUPercent
		memory += event.MemoryPercent
		count++
	}
	if count > 0 {
		cpu /= float64(count)
		memory /= float64(count)
	}
	return cpu, memory, count
}

func (h *telemetryHub) Snapshot() []TelemetryEvent {
	h.mu.RLock()
	events := make([]TelemetryEvent, 0, len(h.latest))
	for _, event := range h.latest {
		events = append(events, event)
	}
	h.mu.RUnlock()
	sort.Slice(events, func(i, j int) bool { return events[i].NodeID < events[j].NodeID })
	return events
}

func telemetryEventFromSample(nodeID string, sample protocol.Telemetry, receivedAt time.Time) TelemetryEvent {
	if sample.Timestamp.IsZero() {
		sample.Timestamp = receivedAt
	}
	event := TelemetryEvent{
		NodeID:           nodeID,
		Timestamp:        sample.Timestamp.UTC(),
		CPUPercent:       clampPercent(sample.CPUPercent),
		MemoryPercent:    clampPercent(sample.MemoryPercent),
		MemoryUsedBytes:  sample.MemoryUsedBytes,
		MemoryTotalBytes: sample.MemoryTotalBytes,
		NetRXBytes:       sample.NetRXBytes,
		NetTXBytes:       sample.NetTXBytes,
	}
	if event.MemoryTotalBytes > 0 && event.MemoryUsedBytes > event.MemoryTotalBytes {
		event.MemoryUsedBytes = event.MemoryTotalBytes
	}
	return event
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

func (s *Server) handleTelemetrySnapshot(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"samples":     s.telemetry.Snapshot(),
		"server_time": time.Now().UTC(),
	})
}

func (s *Server) handleTelemetryEvents(c *gin.Context) {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming is not supported"})
		return
	}

	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache, no-transform")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	events, unsubscribe := s.telemetry.Subscribe()
	defer unsubscribe()

	snapshot, err := json.Marshal(gin.H{"samples": s.telemetry.Snapshot()})
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(c.Writer, "event: telemetry_snapshot\ndata: %s\n\n", snapshot)
	flusher.Flush()

	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			raw, err := json.Marshal(event)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(c.Writer, "event: telemetry_update\ndata: %s\n\n", raw); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(c.Writer, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
