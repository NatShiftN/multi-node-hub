package controlplane

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/cloud"
	"nodehub/internal/store"
)

type cloudPowerRequest struct {
	Action string `json:"action"`
}

func (s *Server) handleCloudPower(c *gin.Context) {
	node, provider, ok := s.cloudNodeProvider(c)
	if !ok {
		return
	}
	var request cloudPowerRequest
	if err := bindJSONLimited(c, &request, 4<<10); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	action := strings.ToLower(strings.TrimSpace(request.Action))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()
	var err error
	switch action {
	case "off", "poweroff", "halt":
		err = provider.PowerOff(ctx, node.ProviderInstanceID)
	case "on", "poweron", "start":
		err = provider.PowerOn(ctx, node.ProviderInstanceID)
	case "reboot":
		err = provider.Reboot(ctx, node.ProviderInstanceID)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "action must be off, on or reboot"})
		return
	}
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "cloud power action failed: " + err.Error()})
		return
	}
	s.audit(c, "cloud_power", node.NodeID, map[string]any{
		"provider": provider.Name(), "action": action, "instance_id": node.ProviderInstanceID,
	})
	c.JSON(http.StatusOK, gin.H{"node_id": node.NodeID, "provider": provider.Name(), "action": action})
}

func (s *Server) handleCloudBandwidth(c *gin.Context) {
	node, provider, ok := s.cloudNodeProvider(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	bandwidth, err := provider.GetBandwidth(ctx, node.ProviderInstanceID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "cloud bandwidth query failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"node_id":   node.NodeID,
		"provider":  provider.Name(),
		"bandwidth": bandwidth,
	})
}

func (s *Server) cloudNodeProvider(c *gin.Context) (store.Node, cloud.Provider, bool) {
	node, err := s.store.GetNode(c.Param("id"))
	if err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return node, nil, false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read node"})
		return node, nil, false
	}
	if strings.EqualFold(node.CloudProvider, "adsl") || strings.TrimSpace(node.CloudProvider) == "" || strings.TrimSpace(node.ProviderInstanceID) == "" {
		c.JSON(http.StatusConflict, gin.H{"error": "node has no cloud provider/instance for this action"})
		return node, nil, false
	}
	provider, ok := s.cloudProviderFor(node.CloudProvider)
	if !ok {
		c.JSON(http.StatusConflict, gin.H{"error": "configured cloud provider is unavailable"})
		return node, nil, false
	}
	return node, provider, true
}
