package controlplane

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/protocol"
	"nodehub/internal/store"
)

const maxPortForwardBody = 16 << 10

type portForwardRuleResponse struct {
	RuleID     string    `json:"rule_id"`
	NodeID     string    `json:"node_id"`
	ListenPort int       `json:"listen_port"`
	Protocol   string    `json:"protocol"`
	TargetHost string    `json:"target_host"`
	TargetPort int       `json:"target_port"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type createPortForwardRequest struct {
	ListenPort int    `json:"listen_port"`
	Protocol   string `json:"protocol"`
	TargetHost string `json:"target_host"`
	TargetPort int    `json:"target_port"`
	Enabled    *bool  `json:"enabled"`
}

type updatePortForwardRequest struct {
	ListenPort *int    `json:"listen_port"`
	Protocol   *string `json:"protocol"`
	TargetHost *string `json:"target_host"`
	TargetPort *int    `json:"target_port"`
	Enabled    *bool   `json:"enabled"`
}

func (s *Server) handleListPortForwards(c *gin.Context) {
	nodeID := c.Param("id")
	if _, err := s.store.GetNode(nodeID); err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read node"})
		return
	}
	rules, err := s.store.ListPortForwardRules(nodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list port-forward rules"})
		return
	}
	views := make([]portForwardRuleResponse, 0, len(rules))
	for _, rule := range rules {
		views = append(views, portForwardRuleResponseFromStore(rule))
	}
	c.JSON(http.StatusOK, gin.H{"rules": views})
}

func (s *Server) handleCreatePortForward(c *gin.Context) {
	nodeID := c.Param("id")
	if _, err := s.store.GetNode(nodeID); err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read node"})
		return
	}

	var req createPortForwardRequest
	if err := bindJSONLimited(c, &req, maxPortForwardBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	listenPort, targetHost, targetPort, err := normalizePortForwardFields(req.ListenPort, req.TargetHost, req.TargetPort)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	protocol, err := normalizeForwardProtocol(req.Protocol)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ruleID, err := newPortForwardRuleID()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate rule id"})
		return
	}
	rule := store.PortForwardRule{
		RuleID:     ruleID,
		NodeID:     nodeID,
		ListenPort: listenPort,
		Protocol:   protocol,
		TargetHost: targetHost,
		TargetPort: targetPort,
		Enabled:    enabled,
	}
	if err := s.store.CreatePortForwardRule(&rule); err != nil {
		if isUniqueConstraint(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "a rule for this listen port already exists on the node"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create port-forward rule"})
		return
	}

	synced, warning := s.syncPortForwardRules(c.Request.Context(), nodeID)
	s.logger.Info("port-forward rule created",
		"node_id", nodeID,
		"rule_id", ruleID,
		"synced", synced,
	)
	status := http.StatusOK
	if !synced {
		status = http.StatusAccepted
	}
	s.audit(c, "port_forward_create", nodeID, map[string]any{
		"rule_id": rule.RuleID, "listen_port": rule.ListenPort, "protocol": rule.Protocol,
		"target_host": rule.TargetHost, "target_port": rule.TargetPort, "enabled": rule.Enabled,
	})
	c.JSON(status, gin.H{
		"rule":    portForwardRuleResponseFromStore(rule),
		"synced":  synced,
		"warning": warning,
	})
}

func (s *Server) handleUpdatePortForward(c *gin.Context) {
	nodeID := c.Param("id")
	ruleID := c.Param("ruleId")
	rule, err := s.store.GetPortForwardRule(nodeID, ruleID)
	if err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "port-forward rule not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read port-forward rule"})
		return
	}

	var req updatePortForwardRequest
	if err := bindJSONLimited(c, &req, maxPortForwardBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.ListenPort != nil {
		rule.ListenPort = *req.ListenPort
	}
	if req.Protocol != nil {
		rule.Protocol = *req.Protocol
	}
	if req.TargetHost != nil {
		rule.TargetHost = *req.TargetHost
	}
	if req.TargetPort != nil {
		rule.TargetPort = *req.TargetPort
	}
	if req.Enabled != nil {
		rule.Enabled = *req.Enabled
	}
	listenPort, targetHost, targetPort, err := normalizePortForwardFields(rule.ListenPort, rule.TargetHost, rule.TargetPort)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	protocol, err := normalizeForwardProtocol(rule.Protocol)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rule.ListenPort = listenPort
	rule.Protocol = protocol
	rule.TargetHost = targetHost
	rule.TargetPort = targetPort

	if err := s.store.SavePortForwardRule(&rule); err != nil {
		if isUniqueConstraint(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "a rule for this listen port already exists on the node"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update port-forward rule"})
		return
	}

	synced, warning := s.syncPortForwardRules(c.Request.Context(), nodeID)
	status := http.StatusOK
	if !synced {
		status = http.StatusAccepted
	}
	s.audit(c, "port_forward_update", nodeID, map[string]any{
		"rule_id": rule.RuleID, "listen_port": rule.ListenPort, "protocol": rule.Protocol,
		"target_host": rule.TargetHost, "target_port": rule.TargetPort, "enabled": rule.Enabled,
	})
	c.JSON(status, gin.H{
		"rule":    portForwardRuleResponseFromStore(rule),
		"synced":  synced,
		"warning": warning,
	})
}

func (s *Server) handleDeletePortForward(c *gin.Context) {
	nodeID := c.Param("id")
	ruleID := c.Param("ruleId")
	if err := s.store.DeletePortForwardRule(nodeID, ruleID); err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "port-forward rule not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete port-forward rule"})
		return
	}

	synced, warning := s.syncPortForwardRules(c.Request.Context(), nodeID)
	status := http.StatusOK
	if !synced {
		status = http.StatusAccepted
	}
	s.audit(c, "port_forward_delete", nodeID, map[string]any{"rule_id": ruleID})
	c.JSON(status, gin.H{
		"deleted": true,
		"synced":  synced,
		"warning": warning,
	})
}

func (s *Server) syncPortForwardsOnConnect(nodeID string) {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.CommandTimeout)
	defer cancel()
	if _, warning := s.syncPortForwardRules(ctx, nodeID); warning != "" {
		s.logger.Warn("sync port-forward rules on connect", "node_id", nodeID, "warning", warning)
	}
}

// syncPortForwardRules sends the complete desired rule list. A false return is
// not an API failure: the rules are persisted and will sync on reconnect.
func (s *Server) syncPortForwardRules(ctx context.Context, nodeID string) (bool, string) {
	if _, online := s.hub.Get(nodeID); !online {
		return false, "node is offline; rules will sync when it reconnects"
	}
	rules, err := s.store.ListPortForwardRules(nodeID)
	if err != nil {
		s.logger.Error("load port-forward rules", "node_id", nodeID, "error", err)
		return false, "rules persisted, but could not be loaded for sync"
	}
	wireRules := make([]protocol.PortForwardRule, 0, len(rules))
	for _, rule := range rules {
		wireRules = append(wireRules, protocol.PortForwardRule{
			RuleID:     rule.RuleID,
			ListenPort: rule.ListenPort,
			Protocol:   rule.Protocol,
			TargetHost: rule.TargetHost,
			TargetPort: rule.TargetPort,
			Enabled:    rule.Enabled,
		})
	}
	payload, err := json.Marshal(protocol.PortForwardPayload{Rules: wireRules})
	if err != nil {
		return false, "rules persisted, but payload encoding failed"
	}

	commandCtx, cancel := context.WithTimeout(ctx, s.cfg.CommandTimeout)
	defer cancel()
	result, _, err := s.hub.Execute(commandCtx, nodeID, "port_forward", payload, s.cfg.CommandTimeout)
	if err != nil {
		s.logger.Warn("apply port-forward rules", "node_id", nodeID, "error", err)
		return false, "rules persisted, but the agent did not acknowledge: " + err.Error()
	}
	if result.Error != "" {
		s.logger.Warn("agent rejected port-forward rules", "node_id", nodeID, "error", result.Error)
		return false, "rules persisted, but the agent rejected them: " + result.Error
	}
	return true, ""
}

func normalizePortForwardFields(listenPort int, targetHost string, targetPort int) (int, string, int, error) {
	if listenPort < 1 || listenPort > 65535 {
		return 0, "", 0, errors.New("listen_port must be between 1 and 65535")
	}
	if targetPort < 1 || targetPort > 65535 {
		return 0, "", 0, errors.New("target_port must be between 1 and 65535")
	}
	host, err := normalizeDeployHost(targetHost)
	if err != nil {
		return 0, "", 0, fmt.Errorf("target_host: %w", err)
	}
	return listenPort, host, targetPort, nil
}

func newPortForwardRuleID() (string, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "pf-" + hex.EncodeToString(raw), nil
}

func normalizeForwardProtocol(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "tcp", nil
	}
	if raw != "tcp" && raw != "udp" {
		return "", errors.New("protocol must be tcp or udp")
	}
	return raw, nil
}

func portForwardRuleResponseFromStore(rule store.PortForwardRule) portForwardRuleResponse {
	protocol := rule.Protocol
	if protocol == "" {
		protocol = "tcp"
	}
	return portForwardRuleResponse{
		RuleID:     rule.RuleID,
		NodeID:     rule.NodeID,
		ListenPort: rule.ListenPort,
		Protocol:   protocol,
		TargetHost: rule.TargetHost,
		TargetPort: rule.TargetPort,
		Enabled:    rule.Enabled,
		CreatedAt:  rule.CreatedAt,
		UpdatedAt:  rule.UpdatedAt,
	}
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint failed") ||
		strings.Contains(message, "constraint failed")
}

func bindJSONLimited(c *gin.Context, target any, limit int64) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	decoder := json.NewDecoder(c.Request.Body)
	if err := decoder.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("request body is required")
		}
		return errors.New("invalid JSON body")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return errors.New("invalid JSON body")
	}
	return nil
}
