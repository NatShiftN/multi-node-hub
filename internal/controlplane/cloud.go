package controlplane

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/cloud"
	"nodehub/internal/ddns"
	"nodehub/internal/store"
)

const replaceIPTimeout = 5 * time.Minute

type cloudSettingsRequest struct {
	CloudProvider      string `json:"cloud_provider"`
	ProviderInstanceID string `json:"provider_instance_id"`
	DDNSDomain         string `json:"ddns_domain"`
	DDNSZoneID         string `json:"ddns_zone_id"`
	DDNSRecordID       string `json:"ddns_record_id"`
	AutoHeal           *bool  `json:"auto_heal"`
}

func (s *Server) cloudProviderFor(name string) (cloud.Provider, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return nil, false
	}
	if credential, err := s.store.GetCloudCredential(name); err == nil {
		key, decryptErr := decryptSecret(s.cfg.JWTSecret, credential.APIKey)
		if decryptErr == nil && strings.TrimSpace(key) != "" {
			switch name {
			case "vultr":
				provider := cloud.NewVultrProvider(key, s.cfg.VultrBaseURL, s.logger)
				provider.OnOrphan = func(instanceID string, orphanErr error) {
					if recordErr := s.store.RecordCloudOrphan("vultr", instanceID, orphanErr.Error(), time.Now().UTC()); recordErr != nil {
						s.logger.Error("record cloud orphan", "instance_id", instanceID, "error", recordErr)
					}
				}
				return provider, true
			case "digitalocean":
				return cloud.NewDigitalOceanProvider(key, s.cfg.DigitalOceanBaseURL, s.logger), true
			}
		}
	}
	return s.cloudProviders.Get(name)
}

func (s *Server) handleUpdateNodeCloud(c *gin.Context) {
	nodeID := c.Param("id")
	existing, err := s.store.GetNode(nodeID)
	if err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read node"})
		return
	}
	var request cloudSettingsRequest
	if err := bindJSONLimited(c, &request, 32<<10); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	autoHeal := existing.AutoHeal
	if request.AutoHeal != nil {
		autoHeal = *request.AutoHeal
	}
	providerName := strings.ToLower(strings.TrimSpace(request.CloudProvider))
	instanceID := strings.TrimSpace(request.ProviderInstanceID)
	if providerName != "" {
		if _, ok := s.cloudProviderFor(providerName); !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unknown cloud provider"})
			return
		}
		if instanceID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "provider_instance_id is required when cloud_provider is set"})
			return
		}
	}
	domain := strings.TrimSpace(request.DDNSDomain)
	zoneID := strings.TrimSpace(request.DDNSZoneID)
	if domain != "" {
		if err := validateDomain(domain); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if zoneID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "ddns_zone_id is required when ddns_domain is set"})
			return
		}
	}
	if err := s.store.UpdateNodeCloudSettings(
		nodeID,
		providerName,
		instanceID,
		domain,
		zoneID,
		strings.TrimSpace(request.DDNSRecordID),
		autoHeal,
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update cloud settings"})
		return
	}
	node, err := s.store.GetNode(nodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to reload node"})
		return
	}
	tags, _ := s.store.ListNodeTags(nodeID)
	s.audit(c, "update_cloud", nodeID, map[string]any{
		"cloud_provider":       node.CloudProvider,
		"provider_instance_id": node.ProviderInstanceID,
		"ddns_domain":          node.DDNSDomain,
		"auto_heal":            node.AutoHeal,
	})
	c.JSON(http.StatusOK, gin.H{"node": nodeResponse(node, time.Now().UTC(), tags)})
}

var (
	errCloudNotConfigured = errors.New("node has no cloud_provider/provider_instance_id configured")
	errCloudUnavailable   = errors.New("configured cloud provider is unavailable")
)

type replaceIPResult struct {
	NodeID        string
	Provider      string
	OldIP         string
	NewIP         string
	NewInstanceID string
	DDNSDomain    string
	DDNSUpdated   bool
	Warning       string
}

func (s *Server) handleReplaceIP(c *gin.Context) {
	nodeID := c.Param("id")
	target, err := s.store.GetNode(nodeID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	if strings.TrimSpace(target.CloudProvider) == "" ||
		(!strings.EqualFold(target.CloudProvider, "adsl") && strings.TrimSpace(target.ProviderInstanceID) == "") {
		c.JSON(http.StatusConflict, gin.H{"error": errCloudNotConfigured.Error()})
		return
	}
	claimed, claimErr := s.store.TryClaimNodeReplacement(nodeID, "manual_replace", "manual", time.Now().UTC(), 0, 2)
	if claimErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to reserve replacement slot"})
		return
	}
	if !claimed {
		go s.notifyHealCircuitOpen(nodeID, "manual_replace")
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": "replacement circuit open: maximum 2 attempts per node in 24h",
		})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), replaceIPTimeout)
	defer cancel()
	result, err := s.replaceNodeIP(ctx, nodeID, "manual")
	if err != nil {
		status := http.StatusBadGateway
		switch {
		case store.IsNotFound(err):
			status = http.StatusNotFound
		case errors.Is(err, errCloudNotConfigured), errors.Is(err, errCloudUnavailable):
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, "replace_ip", result.NodeID, map[string]any{
		"provider":     result.Provider,
		"old_ip":       result.OldIP,
		"new_ip":       result.NewIP,
		"ddns_updated": result.DDNSUpdated,
		"ddns_domain":  result.DDNSDomain,
		"reason":       "manual",
	})
	c.JSON(http.StatusOK, gin.H{
		"node_id":         result.NodeID,
		"provider":        result.Provider,
		"old_ip":          result.OldIP,
		"new_ip":          result.NewIP,
		"new_instance_id": result.NewInstanceID,
		"ddns_updated":    result.DDNSUpdated,
		"ddns_domain":     result.DDNSDomain,
		"warning":         result.Warning,
	})
}

func (s *Server) replaceNodeIP(ctx context.Context, nodeID, reason string) (replaceIPResult, error) {
	node, err := s.store.GetNode(nodeID)
	if err != nil {
		return replaceIPResult{}, err
	}
	if strings.EqualFold(strings.TrimSpace(node.CloudProvider), "adsl") {
		return s.redialADSL(ctx, node, reason)
	}
	if strings.TrimSpace(node.CloudProvider) == "" || strings.TrimSpace(node.ProviderInstanceID) == "" {
		return replaceIPResult{}, errCloudNotConfigured
	}
	provider, ok := s.cloudProviderFor(node.CloudProvider)
	if !ok {
		return replaceIPResult{}, errCloudUnavailable
	}
	oldIP := node.RemoteIP
	var newIP, newInstanceID string
	if replacer, ok := provider.(cloud.InstanceReplacer); ok {
		newIP, newInstanceID, err = replacer.ReplaceIPWithInstance(ctx, node.ProviderInstanceID)
	} else {
		newIP, err = provider.ReplaceIP(ctx, node.ProviderInstanceID)
	}
	if err != nil {
		s.logger.Warn("replace node IP", "node_id", nodeID, "provider", provider.Name(), "reason", reason, "error", err)
		return replaceIPResult{}, errors.New("cloud provider failed: " + err.Error())
	}
	newIP = strings.TrimSpace(newIP)
	if net.ParseIP(newIP) == nil {
		return replaceIPResult{}, errors.New("cloud provider returned an invalid IP")
	}
	if err := s.store.UpdateNodeIPReplacement(nodeID, newIP, newInstanceID); err != nil {
		return replaceIPResult{}, err
	}
	s.logger.Info("node IP replaced",
		"node_id", nodeID,
		"provider", provider.Name(),
		"reason", reason,
		"old_ip", oldIP,
		"new_ip", newIP,
	)
	ddnsUpdated, warning := s.updateNodeDDNS(ctx, nodeID, newIP, reason)
	return replaceIPResult{
		NodeID:        nodeID,
		Provider:      provider.Name(),
		OldIP:         oldIP,
		NewIP:         newIP,
		NewInstanceID: newInstanceID,
		DDNSDomain:    node.DDNSDomain,
		DDNSUpdated:   ddnsUpdated,
		Warning:       warning,
	}, nil
}

func (s *Server) updateNodeDDNS(ctx context.Context, nodeID, ip, reason string) (bool, string) {
	node, err := s.store.GetNode(nodeID)
	if err != nil {
		return false, "node not found for DDNS update"
	}
	if strings.TrimSpace(node.DDNSDomain) == "" || strings.TrimSpace(node.DDNSZoneID) == "" {
		return false, "DDNS is not configured for this node"
	}
	parsedIP := net.ParseIP(strings.TrimSpace(ip))
	if parsedIP == nil || parsedIP.To4() == nil || parsedIP.IsLoopback() || parsedIP.IsPrivate() || parsedIP.IsUnspecified() {
		return false, "DDNS skipped: not a public IPv4 address"
	}
	provider, ok := s.ddnsProviders.Get(s.cfg.DDNSProviderName)
	if !ok {
		return false, "DDNS provider is unavailable"
	}
	ddnsCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = provider.Update(ddnsCtx, ddns.Request{
		ZoneID:   node.DDNSZoneID,
		RecordID: node.DDNSRecordID,
		Domain:   node.DDNSDomain,
		IP:       ip,
	})
	if err != nil {
		s.logger.Warn("update DDNS", "node_id", nodeID, "domain", node.DDNSDomain, "reason", reason, "error", err)
		return false, "DDNS update failed: " + err.Error()
	}
	s.logger.Info("DDNS updated",
		"node_id", nodeID,
		"domain", node.DDNSDomain,
		"ip", ip,
		"reason", reason,
	)
	return true, ""
}

func (s *Server) triggerDDNS(nodeID, ip, reason string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		_, _ = s.updateNodeDDNS(ctx, nodeID, ip, reason)
	}()
}
