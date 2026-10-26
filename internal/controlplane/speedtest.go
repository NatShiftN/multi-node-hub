package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/protocol"
)

const speedtestTimeout = 75 * time.Second

func (s *Server) handleSpeedtest(c *gin.Context) {
	nodeID := c.Param("id")
	view, online := s.hub.Get(nodeID)
	if !online {
		c.JSON(http.StatusConflict, gin.H{"error": "node is offline"})
		return
	}
	supported := false
	for _, plugin := range view.Plugins {
		if plugin == "speedtest" {
			supported = true
			break
		}
	}
	if !supported {
		c.JSON(http.StatusConflict, gin.H{"error": "node agent does not provide the speedtest plugin"})
		return
	}
	if _, loaded := s.speedtestRunning.LoadOrStore(nodeID, struct{}{}); loaded {
		c.JSON(http.StatusConflict, gin.H{"error": "speedtest already running on this node"})
		return
	}
	defer s.speedtestRunning.Delete(nodeID)

	ctx, cancel := context.WithTimeout(c.Request.Context(), speedtestTimeout)
	defer cancel()
	result, elapsed, err := s.hub.Execute(ctx, nodeID, "speedtest", json.RawMessage(`{}`), speedtestTimeout)
	if err != nil {
		c.JSON(http.StatusGatewayTimeout, gin.H{"error": "speedtest failed: " + err.Error()})
		return
	}
	if result.Error != "" {
		c.JSON(http.StatusBadGateway, gin.H{"error": "speedtest failed: " + result.Error})
		return
	}
	if len(result.Data) == 0 {
		c.JSON(http.StatusBadGateway, gin.H{"error": "speedtest returned no result"})
		return
	}
	var stats protocol.SpeedtestResult
	if err := json.Unmarshal(result.Data, &stats); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "invalid speedtest result"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"result":     stats,
		"elapsed_ms": elapsed.Milliseconds(),
	})
}
