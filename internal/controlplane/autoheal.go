package controlplane

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nodehub/internal/notifier"
	"nodehub/internal/store"
)

const autoHealTimeout = 5 * time.Minute

func (s *Server) maybeAutoHeal(nodeID string) {
	if _, loaded := s.autoHealInFlight.LoadOrStore(nodeID, struct{}{}); loaded {
		return
	}
	go func() {
		defer s.autoHealInFlight.Delete(nodeID)
		node, err := s.store.GetNode(nodeID)
		if err != nil {
			s.logger.Error("load node for auto-heal", "node_id", nodeID, "error", err)
			return
		}
		provider := strings.ToLower(strings.TrimSpace(node.CloudProvider))
		if !node.AutoHeal || provider == "" {
			return
		}
		if provider != "adsl" && strings.TrimSpace(node.ProviderInstanceID) == "" {
			return
		}
		claimed, err := s.store.TryClaimAutoHeal(nodeID, time.Now().UTC(), s.cfg.AutoHealCooldown)
		if err != nil {
			s.logger.Error("claim auto-heal cooldown", "node_id", nodeID, "error", err)
			return
		}
		if !claimed {
			count, countErr := s.store.CountNodeHealEvents(nodeID, time.Now().UTC().Add(-24*time.Hour))
			if countErr == nil && count >= 2 {
				s.logger.Error("auto-heal circuit open", "node_id", nodeID, "attempts_24h", count)
				go s.notifyHealCircuitOpen(nodeID, "auto_heal")
			} else {
				s.logger.Info("auto-heal skipped: cooldown active or not configured", "node_id", nodeID)
			}
			return
		}
		s.logger.Warn("auto-heal started", "node_id", nodeID, "provider", node.CloudProvider)
		ctx, cancel := context.WithTimeout(context.Background(), autoHealTimeout)
		defer cancel()
		result, err := s.replaceNodeIP(ctx, nodeID, "auto_heal")
		if err != nil {
			s.logger.Error("auto-heal failed", "node_id", nodeID, "error", err)
			s.auditSystem("system:autoheal", "auto_heal_failed", nodeID, map[string]any{"error": err.Error()})
			s.notify(notifier.Event{
				Type:     "auto_heal_failed",
				Title:    "Auto-heal failed",
				Message:  "The automatic IP replacement did not complete.",
				Severity: "error",
				Fields: map[string]string{
					"node_id": nodeID,
					"error":   err.Error(),
				},
			})
			return
		}
		s.logger.Info("auto-heal succeeded",
			"node_id", nodeID,
			"old_ip", result.OldIP,
			"new_ip", result.NewIP,
			"ddns_updated", result.DDNSUpdated,
		)
		s.auditSystem("system:autoheal", "auto_heal", nodeID, map[string]any{
			"provider":     result.Provider,
			"old_ip":       result.OldIP,
			"new_ip":       result.NewIP,
			"ddns_updated": result.DDNSUpdated,
			"ddns_domain":  result.DDNSDomain,
		})
		s.notify(notifier.Event{
			Type:     "auto_heal_success",
			Title:    "Auto-heal succeeded",
			Message:  "Node IP replaced and DDNS flow completed.",
			Severity: "info",
			Fields: map[string]string{
				"node_id":      nodeID,
				"provider":     result.Provider,
				"old_ip":       result.OldIP,
				"new_ip":       result.NewIP,
				"ddns_domain":  result.DDNSDomain,
				"ddns_updated": fmt.Sprintf("%t", result.DDNSUpdated),
				"warning":      result.Warning,
			},
		})
	}()
}

func (s *Server) notify(event notifier.Event) {
	if s.notifications != nil {
		s.notifications.Notify(event)
	}
}

func (s *Server) notifyNodeOffline(node store.Node, address string) {
	s.notify(notifier.Event{
		Type:     "offline_network_error",
		Title:    "Node network error",
		Message:  "TCP probe failed repeatedly; node marked offline_network_error.",
		Severity: "warning",
		Fields: map[string]string{
			"node_id":   node.NodeID,
			"name":      node.Name,
			"address":   address,
			"auto_heal": fmt.Sprintf("%t", node.AutoHeal),
		},
	})
}

func (s *Server) notifyNodeGFWBlocked(node store.Node, address, detail string) {
	s.notify(notifier.Event{
		Type:     "gfw_blocked",
		Title:    "Node GFW blocked",
		Message:  "Overseas TCP probe succeeded but domestic probes report a blocked TCP path.",
		Severity: "error",
		Fields: map[string]string{
			"node_id":   node.NodeID,
			"name":      node.Name,
			"address":   address,
			"auto_heal": fmt.Sprintf("%t", node.AutoHeal),
			"detail":    detail,
		},
	})
}

func (s *Server) notifyCloudOrphan(providerName, instanceID, detail string) {
	s.notify(notifier.Event{
		Type:     "cloud_orphan",
		Title:    "Cloud orphan needs manual cleanup",
		Message:  "Automatic retry could not delete an orphan cloud instance.",
		Severity: "error",
		Fields: map[string]string{
			"provider": providerName, "instance_id": instanceID, "detail": detail,
		},
	})
}

func (s *Server) notifyHealCircuitOpen(nodeID, action string) {
	node, _ := s.store.GetNode(nodeID)
	s.notify(notifier.Event{
		Type:     "heal_circuit_open",
		Title:    "HIGH RISK: action circuit open",
		Message:  "A node reached the 24h high-risk action limit (2). The operation was blocked to protect the cloud account and node.",
		Severity: "critical",
		Fields: map[string]string{
			"node_id": nodeID,
			"name":    node.Name,
			"action":  action,
			"limit":   "2 per 24h",
		},
	})
	s.auditSystem("system:circuit", "heal_circuit_open", nodeID, map[string]any{"action": action})
}

func (s *Server) notifyTrafficBan(email string, total, quota uint64, nodes int) {
	s.notify(notifier.Event{
		Type:     "traffic_banned",
		Title:    "Traffic quota exceeded",
		Message:  "User exceeded quota and was banned; Xray clients are being removed.",
		Severity: "warning",
		Fields: map[string]string{
			"user_email":  email,
			"total_bytes": fmt.Sprintf("%d", total),
			"quota_bytes": fmt.Sprintf("%d", quota),
			"nodes":       fmt.Sprintf("%d", nodes),
		},
	})
}
