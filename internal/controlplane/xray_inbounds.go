package controlplane

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/store"
	xraygen "nodehub/internal/xray_generator"
)

const maxProxyInboundBody = 64 << 10

type proxyInboundRequest struct {
	Remark   string          `json:"remark"`
	Port     int             `json:"port"`
	Protocol string          `json:"protocol"`
	Network  string          `json:"network"`
	Security string          `json:"security"`
	Settings json.RawMessage `json:"settings"`
}

type proxyInboundView struct {
	ID        uint             `json:"id"`
	NodeID    string           `json:"node_id"`
	Remark    string           `json:"remark"`
	Port      int              `json:"port"`
	Protocol  string           `json:"protocol"`
	Network   string           `json:"network"`
	Security  string           `json:"security"`
	Settings  xraygen.Settings `json:"settings"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

func (s *Server) handleListProxyInbounds(c *gin.Context) {
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
	inbounds, err := s.store.ListProxyInbounds(nodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list inbounds"})
		return
	}
	views := make([]proxyInboundView, 0, len(inbounds))
	for _, inbound := range inbounds {
		view, err := proxyInboundViewFromStore(inbound)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid stored inbound settings"})
			return
		}
		views = append(views, view)
	}
	c.JSON(http.StatusOK, gin.H{
		"inbounds":  views,
		"node_host": nodePublicHost(node),
	})
}

func (s *Server) handleCreateProxyInbound(c *gin.Context) {
	nodeID := c.Param("id")
	if _, err := s.store.GetNode(nodeID); err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read node"})
		return
	}
	var request proxyInboundRequest
	if err := bindJSONLimited(c, &request, maxProxyInboundBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	inbound, err := normalizedInbound(0, request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	settingsJSON, _ := json.Marshal(inbound.Settings)
	record := store.ProxyInbound{
		NodeID:       nodeID,
		Remark:       inbound.Remark,
		Port:         inbound.Port,
		Protocol:     inbound.Protocol,
		Network:      inbound.Network,
		Security:     inbound.Security,
		SettingsJSON: string(settingsJSON),
	}

	s.xrayInboundMu.Lock()
	defer s.xrayInboundMu.Unlock()
	if err := s.store.CreateProxyInbound(&record); err != nil {
		if isUniqueConstraint(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "port is already used by another inbound on this node"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save inbound"})
		return
	}
	synced, warning, err := s.regenerateXray(c.Request.Context(), nodeID)
	if err != nil {
		_ = s.store.DeleteProxyInbound(nodeID, record.ID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate Xray config: " + err.Error()})
		return
	}
	if saved, loadErr := s.store.GetProxyInbound(nodeID, record.ID); loadErr == nil {
		record = saved
	}
	s.audit(c, "inbound_create", nodeID, inboundAuditDetails(record, synced))
	view, _ := proxyInboundViewFromStore(record)
	c.JSON(syncStatus(synced), gin.H{
		"inbound": view,
		"synced":  synced,
		"warning": warning,
	})
}

func (s *Server) handleUpdateProxyInbound(c *gin.Context) {
	nodeID := c.Param("id")
	id, err := strconv.ParseUint(c.Param("inboundId"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid inbound id"})
		return
	}
	var request proxyInboundRequest
	if err := bindJSONLimited(c, &request, maxProxyInboundBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	inbound, err := normalizedInbound(uint(id), request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	s.xrayInboundMu.Lock()
	defer s.xrayInboundMu.Unlock()
	existing, err := s.store.GetProxyInbound(nodeID, uint(id))
	if err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "inbound not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read inbound"})
		return
	}
	settingsJSON, _ := json.Marshal(inbound.Settings)
	record := store.ProxyInbound{
		ID:           existing.ID,
		NodeID:       nodeID,
		Remark:       inbound.Remark,
		Port:         inbound.Port,
		Protocol:     inbound.Protocol,
		Network:      inbound.Network,
		Security:     inbound.Security,
		SettingsJSON: string(settingsJSON),
	}
	if err := s.store.UpdateProxyInbound(&record); err != nil {
		if isUniqueConstraint(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "port is already used by another inbound on this node"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update inbound"})
		return
	}
	synced, warning, err := s.regenerateXray(c.Request.Context(), nodeID)
	if err != nil {
		_ = s.store.UpdateProxyInbound(&existing)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate Xray config: " + err.Error()})
		return
	}
	if saved, loadErr := s.store.GetProxyInbound(nodeID, record.ID); loadErr == nil {
		record = saved
	}
	s.audit(c, "inbound_update", nodeID, inboundAuditDetails(record, synced))
	view, _ := proxyInboundViewFromStore(record)
	c.JSON(syncStatus(synced), gin.H{
		"inbound": view,
		"synced":  synced,
		"warning": warning,
	})
}

func (s *Server) handleDeleteProxyInbound(c *gin.Context) {
	nodeID := c.Param("id")
	id, err := strconv.ParseUint(c.Param("inboundId"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid inbound id"})
		return
	}
	s.xrayInboundMu.Lock()
	defer s.xrayInboundMu.Unlock()
	existing, err := s.store.GetProxyInbound(nodeID, uint(id))
	if err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "inbound not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read inbound"})
		return
	}
	if err := s.store.DeleteProxyInbound(nodeID, uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete inbound"})
		return
	}
	synced, warning, err := s.regenerateXray(c.Request.Context(), nodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate Xray config: " + err.Error()})
		return
	}
	s.audit(c, "inbound_delete", nodeID, inboundAuditDetails(existing, synced))
	c.JSON(syncStatus(synced), gin.H{
		"deleted": true,
		"synced":  synced,
		"warning": warning,
	})
}

func (s *Server) handleRealityKeys(c *gin.Context) {
	nodeID := c.Param("id")
	if _, err := s.store.GetNode(nodeID); err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read node"})
		return
	}
	keys, err := xraygen.GenerateRealityKeys()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate Reality keys"})
		return
	}
	c.JSON(http.StatusOK, keys)
}

func (s *Server) handleInboundShare(c *gin.Context) {
	nodeID := c.Param("id")
	id, err := strconv.ParseUint(c.Param("inboundId"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid inbound id"})
		return
	}
	node, err := s.store.GetNode(nodeID)
	if err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read node"})
		return
	}
	record, err := s.store.GetProxyInbound(nodeID, uint(id))
	if err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "inbound not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read inbound"})
		return
	}
	inbound, err := generatorInboundFromStore(record)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid stored inbound settings"})
		return
	}
	host := nodePublicHost(node)
	link, err := xraygen.ShareLink(inbound, host)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"link":     link,
		"host":     host,
		"port":     inbound.Port,
		"protocol": inbound.Protocol,
		"remark":   inbound.Remark,
	})
}

func (s *Server) handleNodeSubscription(c *gin.Context) {
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
	records, err := s.store.ListProxyInbounds(nodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list inbounds"})
		return
	}
	host := nodePublicHost(node)
	links := make([]string, 0, len(records))
	for _, record := range records {
		inbound, err := generatorInboundFromStore(record)
		if err != nil {
			continue
		}
		link, err := xraygen.ShareLink(inbound, host)
		if err == nil {
			links = append(links, link)
		}
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))
	c.Header("Profile-Update-Interval", "24")
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(encoded))
}

func normalizedInbound(id uint, request proxyInboundRequest) (xraygen.Inbound, error) {
	if len(request.Settings) == 0 || string(request.Settings) == "null" {
		return xraygen.Inbound{}, errors.New("settings object is required")
	}
	var settings xraygen.Settings
	decoder := json.NewDecoder(strings.NewReader(string(request.Settings)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		return xraygen.Inbound{}, fmt.Errorf("invalid settings: %w", err)
	}
	return xraygen.Normalize(xraygen.Inbound{
		ID:       id,
		Remark:   request.Remark,
		Port:     request.Port,
		Protocol: request.Protocol,
		Network:  request.Network,
		Security: request.Security,
		Settings: settings,
	})
}

func (s *Server) regenerateXray(_ context.Context, nodeID string) (bool, string, error) {
	records, err := s.store.ListProxyInbounds(nodeID)
	if err != nil {
		return false, "", err
	}
	if len(records) == 0 {
		if err := s.store.DeleteXrayConfig(nodeID); err != nil && !store.IsNotFound(err) {
			return false, "", err
		}
		s.enqueueXraySync(nodeID, "", false)
		return false, "Xray config removed; Agent apply is running in the background", nil
	}
	inbounds := make([]xraygen.Inbound, 0, len(records))
	for _, record := range records {
		inbound, err := generatorInboundFromStore(record)
		if err != nil {
			return false, "", err
		}
		inbounds = append(inbounds, inbound)
	}
	config, err := xraygen.Generate(inbounds)
	if err != nil {
		return false, "", err
	}
	if err := s.store.SaveXrayConfig(nodeID, string(config)); err != nil {
		return false, "", err
	}
	s.enqueueXraySync(nodeID, string(config), true)
	return false, "Xray config saved; Agent apply is running in the background", nil
}

func (s *Server) enqueueXraySync(nodeID, configJSON string, enabled bool) {
	go func() {
		s.xraySyncMu.Lock()
		defer s.xraySyncMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), xrayCommandTimeout+5*time.Second)
		defer cancel()
		synced, warning := s.syncXrayConfig(ctx, nodeID, configJSON, enabled)
		if !synced {
			s.logger.Warn("background Xray sync incomplete", "node_id", nodeID, "warning", warning)
			return
		}
		s.logger.Info("background Xray sync completed", "node_id", nodeID, "enabled", enabled)
	}()
}

func generatorInboundFromStore(record store.ProxyInbound) (xraygen.Inbound, error) {
	var settings xraygen.Settings
	if err := json.Unmarshal([]byte(record.SettingsJSON), &settings); err != nil {
		return xraygen.Inbound{}, err
	}
	return xraygen.Inbound{
		ID:       record.ID,
		Remark:   record.Remark,
		Port:     record.Port,
		Protocol: record.Protocol,
		Network:  record.Network,
		Security: record.Security,
		Settings: settings,
	}, nil
}

func proxyInboundViewFromStore(record store.ProxyInbound) (proxyInboundView, error) {
	inbound, err := generatorInboundFromStore(record)
	if err != nil {
		return proxyInboundView{}, err
	}
	return proxyInboundView{
		ID:        record.ID,
		NodeID:    record.NodeID,
		Remark:    record.Remark,
		Port:      record.Port,
		Protocol:  record.Protocol,
		Network:   record.Network,
		Security:  record.Security,
		Settings:  inbound.Settings,
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
	}, nil
}

func inboundAuditDetails(record store.ProxyInbound, synced bool) map[string]any {
	return map[string]any{
		"port":     record.Port,
		"protocol": record.Protocol,
		"network":  record.Network,
		"security": record.Security,
		"remark":   record.Remark,
		"synced":   synced,
	}
}

func syncStatus(synced bool) int {
	if synced {
		return http.StatusOK
	}
	return http.StatusAccepted
}

func nodePublicHost(node store.Node) string {
	candidates := []string{node.DDNSDomain, node.Address, node.RemoteIP}
	normalized := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if host, _, err := net.SplitHostPort(candidate); err == nil {
			candidate = host
		}
		candidate = strings.Trim(candidate, "[]")
		if candidate != "" {
			normalized = append(normalized, candidate)
		}
	}
	for _, candidate := range normalized {
		if ip := net.ParseIP(candidate); ip == nil || !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() {
			return candidate
		}
	}
	if len(normalized) > 0 {
		return normalized[0]
	}
	return ""
}
