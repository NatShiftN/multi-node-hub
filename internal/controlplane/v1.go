package controlplane

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/store"
)

type v1Node struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Address      string `json:"address"`
	RemoteIP     string `json:"remote_ip,omitempty"`
	Status       string `json:"status"`
	AgentVersion string `json:"agent_version,omitempty"`
	OS           string `json:"os,omitempty"`
	Arch         string `json:"arch,omitempty"`
}

type v1TrafficAllocateRequest struct {
	Email       string   `json:"email"`
	QuotaBytes  uint64   `json:"quota_bytes"`
	DeviceLimit int      `json:"device_limit"`
	NodeIDs     []string `json:"node_ids,omitempty"`
	ResetUsage  bool     `json:"reset_usage,omitempty"`
}

// @Summary List nodes available to the merchant
// @Tags OpenAPI
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]any
// @Router /v1/nodes [get]
func (s *Server) handleV1ListNodes(c *gin.Context) {
	nodes, err := s.store.ListNodes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list nodes"})
		return
	}
	views := make([]v1Node, 0, len(nodes))
	for _, node := range nodes {
		views = append(views, v1Node{
			ID: node.NodeID, Name: node.Name, Address: node.Address, RemoteIP: node.RemoteIP,
			Status: node.Status, AgentVersion: node.AgentVersion, OS: node.OS, Arch: node.Arch,
		})
	}
	c.JSON(http.StatusOK, gin.H{"nodes": views, "server_time": time.Now().UTC()})
}

// @Summary Get one node
// @Tags OpenAPI
// @Security BearerAuth
// @Produce json
// @Param id path string true "Node ID"
// @Success 200 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Router /v1/nodes/{id} [get]
func (s *Server) handleV1GetNode(c *gin.Context) {
	node, err := s.store.GetNode(c.Param("id"))
	if err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read node"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"node": v1Node{
		ID: node.NodeID, Name: node.Name, Address: node.Address, RemoteIP: node.RemoteIP,
		Status: node.Status, AgentVersion: node.AgentVersion, OS: node.OS, Arch: node.Arch,
	}})
}

// @Summary Allocate traffic quota and device limit
// @Description Creates or updates a merchant user's quota, clears a previous ban, and optionally resets usage.
// @Tags OpenAPI
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body v1TrafficAllocateRequest true "Allocation"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]any
// @Router /v1/traffic/allocate [post]
func (s *Server) handleV1AllocateTraffic(c *gin.Context) {
	var request v1TrafficAllocateRequest
	if err := bindJSONLimited(c, &request, 16<<10); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	email := strings.TrimSpace(request.Email)
	if email == "" || len(email) > 255 || !strings.Contains(email, "@") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "valid email is required"})
		return
	}
	if request.DeviceLimit < 0 || request.DeviceLimit > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "device_limit must be between 0 and 1000"})
		return
	}
	for _, nodeID := range request.NodeIDs {
		if _, err := s.store.GetNode(strings.TrimSpace(nodeID)); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unknown node_id: " + nodeID})
			return
		}
	}
	user, err := s.store.AllocateTraffic(email, request.QuotaBytes, request.DeviceLimit, request.NodeIDs, request.ResetUsage)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to allocate traffic"})
		return
	}
	keyName := c.GetString("api_key_name")
	s.auditSystem("apikey:"+keyName, "traffic_allocate", "", map[string]any{
		"email":        email,
		"quota_bytes":  request.QuotaBytes,
		"device_limit": request.DeviceLimit,
		"node_ids":     request.NodeIDs,
		"reset_usage":  request.ResetUsage,
	})
	c.JSON(http.StatusOK, gin.H{
		"user": map[string]any{
			"email":        user.UserEmail,
			"quota_bytes":  user.QuotaBytes,
			"device_limit": user.DeviceLimit,
			"is_banned":    user.IsBanned,
		},
	})
}

// @Summary Get merchant traffic state
// @Tags OpenAPI
// @Security BearerAuth
// @Produce json
// @Param email path string true "User email"
// @Success 200 {object} map[string]any
// @Router /v1/traffic/users/{email} [get]
func (s *Server) handleV1GetTrafficUser(c *gin.Context) {
	email := strings.TrimSpace(c.Param("email"))
	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}
	summaries, err := s.store.ListTrafficUserSummaries()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list traffic users"})
		return
	}
	for _, summary := range summaries {
		if summary.UserEmail == email {
			c.JSON(http.StatusOK, gin.H{"user": summary})
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "traffic user not found"})
}
