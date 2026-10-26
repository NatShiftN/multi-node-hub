package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/store"
)

const maxCommandBody = 64 << 10

var pluginNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type commandRequest struct {
	Plugin  string          `json:"plugin"`
	Payload json.RawMessage `json:"payload"`
}

func (s *Server) handleListNodes(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	result, err := s.store.ListNodesPage(
		page,
		limit,
		c.Query("search"),
		c.Query("status"),
		c.Query("tag"),
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list nodes"})
		return
	}
	now := time.Now().UTC()
	tagMap, err := s.store.ListAllNodeTags()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list node tags"})
		return
	}
	views := make([]NodeResponse, 0, len(result.Nodes))
	for _, node := range result.Nodes {
		view := nodeResponse(node, now, tagMap[node.NodeID])
		if sample, ok := s.telemetry.Get(node.NodeID); ok {
			view.CPUPercent = sample.CPUPercent
			view.MemoryPercent = sample.MemoryPercent
			view.CPUHistory = s.telemetry.History(node.NodeID)
		}
		if view.CPUHistory == nil {
			view.CPUHistory = []float64{}
		}
		views = append(views, view)
	}
	totalPages := int64(0)
	if result.Limit > 0 {
		totalPages = (result.Total + int64(result.Limit) - 1) / int64(result.Limit)
	}
	c.JSON(http.StatusOK, gin.H{
		"nodes":       views,
		"server_time": now,
		"total":       result.Total,
		"page":        result.Page,
		"limit":       result.Limit,
		"total_pages": totalPages,
	})
}

func (s *Server) handleGetNode(c *gin.Context) {
	nodeID := c.Param("id")
	node, err := s.store.GetNode(nodeID)
	if err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read node"})
		return
	}
	tags, err := s.store.ListNodeTags(nodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read node tags"})
		return
	}
	view := nodeResponse(node, time.Now().UTC(), tags)
	if sample, ok := s.telemetry.Get(nodeID); ok {
		view.CPUPercent = sample.CPUPercent
		view.MemoryPercent = sample.MemoryPercent
		view.CPUHistory = s.telemetry.History(nodeID)
	}
	if view.CPUHistory == nil {
		view.CPUHistory = []float64{}
	}
	c.JSON(http.StatusOK, gin.H{
		"node":        view,
		"server_time": time.Now().UTC(),
	})
}

func (s *Server) handleUpdateNodeIdentity(c *gin.Context) {
	nodeID := c.Param("id")
	var request struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	}
	if err := bindJSONLimited(c, &request, 8<<10); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	name := strings.TrimSpace(request.Name)
	if name == "" || len(name) > 255 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name must be 1-255 characters"})
		return
	}
	address, err := normalizeDeployHost(request.Address)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "address: " + err.Error()})
		return
	}
	if err := s.store.UpdateNodeIdentity(nodeID, name, address); err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update node identity"})
		return
	}
	s.audit(c, "node_identity_update", nodeID, map[string]any{"name": name, "address": address})
	node, err := s.store.GetNode(nodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read node"})
		return
	}
	tags, _ := s.store.ListNodeTags(nodeID)
	c.JSON(http.StatusOK, gin.H{"node": nodeResponse(node, time.Now().UTC(), tags)})
}

func (s *Server) handleDeleteNode(c *gin.Context) {
	nodeID := c.Param("id")
	if _, err := s.store.GetNode(nodeID); err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read node"})
		return
	}
	s.hub.Disconnect(nodeID)
	s.tokens.RevokeNode(nodeID)
	if err := s.store.DecommissionNode(nodeID, time.Now().UTC()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decommission node"})
		return
	}
	if err := s.store.DeleteNode(nodeID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete node"})
		return
	}
	s.audit(c, "delete_node", nodeID, map[string]any{"deleted": true, "decommissioned": true})
	c.JSON(http.StatusOK, gin.H{"deleted": true, "node_id": nodeID})
}

func (s *Server) handleCommand(c *gin.Context) {
	nodeID := c.Param("id")
	if nodeID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "node id is required"})
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCommandBody)
	var req commandRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		if errors.Is(err, io.EOF) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "request body is required"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body: " + err.Error()})
		return
	}
	if !pluginNamePattern.MatchString(req.Plugin) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "plugin must match [A-Za-z0-9][A-Za-z0-9._-]{0,63}"})
		return
	}
	if c.GetString("admin_role") == store.RoleOperator && isHighRiskPlugin(req.Plugin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "operator cannot run destructive plugins"})
		return
	}
	if len(req.Payload) > 0 && !json.Valid(req.Payload) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "payload must be valid JSON"})
		return
	}
	if _, err := s.store.GetNode(nodeID); err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read node"})
		return
	}
	if _, online := s.hub.Get(nodeID); !online {
		c.JSON(http.StatusConflict, gin.H{"error": "node is offline", "node_id": nodeID})
		return
	}

	result, elapsed, err := s.hub.Execute(c.Request.Context(), nodeID, req.Plugin, req.Payload, s.cfg.CommandTimeout)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, ErrNodeNotFound):
			status = http.StatusNotFound
		case errors.Is(err, ErrNodeOffline):
			status = http.StatusConflict
		case errors.Is(err, ErrCommandTimeout), errors.Is(err, context.DeadlineExceeded):
			status = http.StatusGatewayTimeout
		case errors.Is(err, context.Canceled):
			status = http.StatusRequestTimeout
		}
		c.JSON(status, gin.H{"error": err.Error(), "node_id": nodeID})
		return
	}

	if result.Error != "" {
		c.JSON(http.StatusBadGateway, gin.H{
			"error":      result.Error,
			"request_id": result.RequestID,
			"node_id":    nodeID,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"request_id":  result.RequestID,
		"node_id":     nodeID,
		"plugin":      result.Plugin,
		"duration_ms": elapsed.Milliseconds(),
		"data":        result.Data,
	})
}
