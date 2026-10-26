package controlplane

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func (s *Server) seedDemo() error {
	return s.store.SeedDemoData()
}

func (s *Server) demoGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		case http.MethodPost:
			if c.Request.URL.Path == "/api/login" {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "demo mode is read-only; start without --demo for write access",
		})
	}
}

func (s *Server) runDemoTelemetry(ctx context.Context) {
	nodes := []struct {
		id   string
		cpu  float64
		mem  float64
		rx   uint64
		tx   uint64
		base float64
	}{
		{"demo-hk", 18, 42, 8_400_000_000, 3_200_000_000, 0.2},
		{"demo-sg", 27, 51, 12_100_000_000, 5_700_000_000, 1.7},
		{"demo-us", 12, 36, 5_600_000_000, 2_100_000_000, 3.1},
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	started := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			elapsed := now.Sub(started).Seconds()
			for _, node := range nodes {
				cpu := node.cpu + 9*math.Sin(elapsed/23+node.base)
				mem := node.mem + 3*math.Sin(elapsed/41+node.base)
				s.telemetry.Publish(TelemetryEvent{
					NodeID: node.id, Timestamp: now.UTC(),
					CPUPercent: clampPercent(cpu), MemoryPercent: clampPercent(mem),
					MemoryUsedBytes: uint64(mem / 100 * float64(8<<30)), MemoryTotalBytes: 8 << 30,
					NetRXBytes: node.rx + uint64(elapsed*1_800_000),
					NetTXBytes: node.tx + uint64(elapsed*700_000),
				})
			}
		}
	}
}
