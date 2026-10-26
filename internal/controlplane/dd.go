package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/protocol"
	"nodehub/internal/store"
)

var ddVersionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

type ddRequest struct {
	Distro       string `json:"distro"`
	Version      string `json:"version"`
	Arch         string `json:"arch"`
	RootPassword string `json:"root_password,omitempty"`
}

func (s *Server) handleDDNode(c *gin.Context) {
	node, err := s.store.GetNode(c.Param("id"))
	if err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read node"})
		return
	}
	if _, online := s.hub.Get(node.NodeID); !online {
		c.JSON(http.StatusConflict, gin.H{"error": "one-click DD requires an online Agent"})
		return
	}
	var request ddRequest
	if err := bindJSONLimited(c, &request, 8<<10); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	distro := strings.ToLower(strings.TrimSpace(request.Distro))
	if distro == "" {
		distro = "debian"
	}
	if distro != "debian" && distro != "ubuntu" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "distro must be debian or ubuntu"})
		return
	}
	version := strings.TrimSpace(request.Version)
	if version == "" {
		if distro == "debian" {
			version = "12"
		} else {
			version = "22.04"
		}
	}
	if !ddVersionPattern.MatchString(version) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid OS version"})
		return
	}
	arch := strings.ToLower(strings.TrimSpace(request.Arch))
	if arch == "" {
		arch = "amd64"
	}
	if arch != "amd64" && arch != "arm64" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "arch must be amd64 or arm64"})
		return
	}
	if len(request.RootPassword) > 128 || strings.ContainsAny(request.RootPassword, "\r\n") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid root password"})
		return
	}

	_, agentURL, wsURL, err := s.deploymentEndpoints()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build deployment endpoints"})
		return
	}
	token := s.tokens.StaticToken()
	warning := ""
	if token == "" {
		token, err = s.tokens.Issue(node.NodeID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to issue DD bootstrap token"})
			return
		}
		warning = "no shared AGENT_TOKEN configured; the node token was rotated. If DD fails, redeploy the Agent."
	}
	payload, err := json.Marshal(protocol.DDPayload{
		NodeID:       node.NodeID,
		Token:        token,
		ServerURL:    wsURL,
		AgentURL:     agentURL,
		Distro:       distro,
		Version:      version,
		Arch:         arch,
		RootPassword: request.RootPassword,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode DD payload"})
		return
	}
	claimed, claimErr := s.store.TryClaimNodeReplacement(node.NodeID, "dd_reinstall", "one_click_dd", time.Now().UTC(), 0, 2)
	if claimErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to reserve high-risk action slot"})
		return
	}
	if !claimed {
		go s.notifyHealCircuitOpen(node.NodeID, "dd_reinstall")
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": "high-risk action circuit open: maximum 2 DD/replacement attempts per node in 24h",
		})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()
	result, _, err := s.hub.Execute(ctx, node.NodeID, "dd", payload, 90*time.Second)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to start DD: " + err.Error()})
		return
	}
	if result.Error != "" {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Agent rejected DD: " + result.Error})
		return
	}
	s.audit(c, "dd_reinstall", node.NodeID, map[string]any{
		"distro": distro, "version": version, "arch": arch,
	})
	c.JSON(http.StatusAccepted, gin.H{
		"accepted": true,
		"node_id":  node.NodeID,
		"distro":   distro,
		"version":  version,
		"arch":     arch,
		"warning":  warning,
	})
}
