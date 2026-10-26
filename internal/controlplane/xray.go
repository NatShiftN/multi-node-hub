package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/protocol"
	"nodehub/internal/store"
)

const (
	maxXrayConfigBody  = 2 << 20
	xrayCommandTimeout = 3 * time.Minute
)

type xrayConfigRequest struct {
	Config json.RawMessage `json:"config"`
}

type xrayConfigResponse struct {
	NodeID    string          `json:"node_id"`
	Config    json.RawMessage `json:"config"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func (s *Server) handleGetXrayConfig(c *gin.Context) {
	nodeID := c.Param("id")
	if _, err := s.store.GetNode(nodeID); err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read node"})
		return
	}
	config, err := s.store.GetXrayConfig(nodeID)
	if err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "xray config not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read xray config"})
		return
	}
	c.JSON(http.StatusOK, xrayConfigResponse{
		NodeID:    config.NodeID,
		Config:    json.RawMessage(config.Config),
		UpdatedAt: config.UpdatedAt,
	})
}

func (s *Server) handlePutXrayConfig(c *gin.Context) {
	nodeID := c.Param("id")
	if _, err := s.store.GetNode(nodeID); err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read node"})
		return
	}

	var request xrayConfigRequest
	if err := bindJSONLimited(c, &request, maxXrayConfigBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	configJSON, err := normalizeControlPlaneXrayConfig(request.Config)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := s.store.SaveXrayConfig(nodeID, configJSON); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save xray config"})
		return
	}
	saved, err := s.store.GetXrayConfig(nodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to reload xray config"})
		return
	}

	synced, warning := s.syncXrayConfig(c.Request.Context(), nodeID, configJSON, true)
	status := http.StatusOK
	if !synced {
		status = http.StatusAccepted
	}
	s.audit(c, "xray_update", nodeID, map[string]any{
		"config_bytes": len(saved.Config),
		"synced":       synced,
	})
	c.JSON(status, gin.H{
		"config":     json.RawMessage(saved.Config),
		"updated_at": saved.UpdatedAt,
		"synced":     synced,
		"warning":    warning,
	})
}

func (s *Server) handleXrayStatus(c *gin.Context) {
	nodeID := c.Param("id")
	if _, online := s.hub.Get(nodeID); !online {
		c.JSON(http.StatusConflict, gin.H{"error": "agent is not connected"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	result, elapsed, err := s.hub.Execute(ctx, nodeID, "xray", []byte(`{"action":"status"}`), 15*time.Second)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if result.Error != "" {
		c.JSON(http.StatusBadGateway, gin.H{"error": result.Error})
		return
	}
	c.JSON(http.StatusOK, gin.H{"node_id": nodeID, "status": result.Data, "duration_ms": elapsed.Milliseconds()})
}

func (s *Server) handleXrayRestart(c *gin.Context) {
	nodeID := c.Param("id")
	config, err := s.store.GetXrayConfig(nodeID)
	if err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "xray config not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read xray config"})
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), xrayCommandTimeout+5*time.Second)
		defer cancel()
		synced, warning := s.syncXrayConfig(ctx, nodeID, config.Config, true)
		if !synced {
			s.logger.Warn("Xray restart incomplete", "node_id", nodeID, "warning", warning)
		}
	}()
	s.audit(c, "xray_restart", nodeID, map[string]any{"background": true})
	c.JSON(http.StatusAccepted, gin.H{"accepted": true, "node_id": nodeID})
}

func (s *Server) handleDeleteXrayConfig(c *gin.Context) {
	nodeID := c.Param("id")
	if err := s.store.DeleteXrayConfig(nodeID); err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "xray config not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete xray config"})
		return
	}
	synced, warning := s.syncXrayConfig(c.Request.Context(), nodeID, "", false)
	status := http.StatusOK
	if !synced {
		status = http.StatusAccepted
	}
	s.audit(c, "xray_delete", nodeID, map[string]any{"synced": synced})
	c.JSON(status, gin.H{
		"deleted": true,
		"synced":  synced,
		"warning": warning,
	})
}

func (s *Server) syncXrayOnConnect(nodeID string) {
	ctx, cancel := context.WithTimeout(context.Background(), xrayCommandTimeout+5*time.Second)
	defer cancel()

	config, err := s.store.GetXrayConfig(nodeID)
	if err != nil {
		if !store.IsNotFound(err) {
			s.logger.Warn("load Xray config on connect", "node_id", nodeID, "error", err)
			return
		}
		if _, warning := s.syncXrayConfig(ctx, nodeID, "", false); warning != "" {
			s.logger.Warn("stop Xray on connect", "node_id", nodeID, "warning", warning)
		}
		return
	}
	if _, warning := s.syncXrayConfig(ctx, nodeID, config.Config, true); warning != "" {
		s.logger.Warn("sync Xray config on connect", "node_id", nodeID, "warning", warning)
	}
}

func (s *Server) syncXrayConfig(ctx context.Context, nodeID, configJSON string, enabled bool) (bool, string) {
	if _, online := s.hub.Get(nodeID); !online {
		return false, "node is offline; Xray config will sync when it reconnects"
	}
	desired := protocol.XrayPayload{Enabled: enabled}
	if enabled {
		desired.Config = json.RawMessage(configJSON)
	}
	payload, err := json.Marshal(desired)
	if err != nil {
		return false, "Xray config saved, but payload encoding failed"
	}
	commandCtx, cancel := context.WithTimeout(ctx, xrayCommandTimeout)
	defer cancel()
	result, _, err := s.hub.Execute(commandCtx, nodeID, "xray", payload, xrayCommandTimeout)
	if err != nil {
		s.logger.Warn("apply Xray config", "node_id", nodeID, "error", err)
		return false, "Xray config saved, but the agent did not acknowledge: " + err.Error()
	}
	if result.Error != "" {
		s.logger.Warn("agent rejected Xray config", "node_id", nodeID, "error", result.Error)
		return false, "Xray config saved, but the agent rejected it: " + result.Error
	}
	return true, ""
}

func normalizeControlPlaneXrayConfig(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", errors.New("xray config is required")
	}
	if len(raw) > maxXrayConfigBody {
		return "", fmt.Errorf("xray config exceeds %d bytes", maxXrayConfigBody)
	}
	value := bytes.TrimSpace(raw)
	if len(value) > 0 && value[0] == '"' {
		var encoded string
		if err := json.Unmarshal(value, &encoded); err != nil {
			return "", errors.New("xray config string is invalid")
		}
		value = bytes.TrimSpace([]byte(encoded))
	}
	if len(value) == 0 || value[0] != '{' || !json.Valid(value) {
		return "", errors.New("xray config must be a valid JSON object")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, value); err != nil {
		return "", errors.New("xray config is invalid")
	}
	return compact.String(), nil
}
