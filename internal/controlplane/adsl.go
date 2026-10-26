package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"nodehub/internal/store"
)

const (
	adslRedialCommandTimeout = 45 * time.Second
	adslRedialWait           = 2 * time.Minute
	adslPollInterval         = 2 * time.Second
)

// redialADSL asks the edge side to re-dial the PPPoE session. The command may
// tear down the WebSocket before its result can be delivered, so success is
// confirmed by observing the Agent reconnect with a new RemoteIP rather than
// by trusting the command response alone.
func (s *Server) redialADSL(ctx context.Context, node store.Node, reason string) (replaceIPResult, error) {
	view, online := s.hub.Get(node.NodeID)
	if !online {
		return replaceIPResult{}, errors.New("ADSL redial requires an online Agent")
	}
	if !containsString(view.Plugins, "pppoe_redial") {
		return replaceIPResult{}, errors.New("Agent does not expose pppoe_redial; upgrade Agent")
	}
	oldIP := strings.TrimSpace(node.RemoteIP)
	payload, _ := json.Marshal(map[string]any{"reason": reason})
	commandCtx, cancel := context.WithTimeout(ctx, adslRedialCommandTimeout)
	result, _, err := s.hub.Execute(commandCtx, node.NodeID, "pppoe_redial", payload, adslRedialCommandTimeout)
	cancel()
	reportedIP := ""
	if err != nil {
		// Losing the WebSocket as PPPoE goes down is expected.
		s.logger.Info("ADSL redial command disconnected or failed; waiting for Agent reconnect",
			"node_id", node.NodeID,
			"reason", reason,
			"error", err,
		)
	} else if result.Error != "" {
		s.logger.Warn("ADSL redial command rejected", "node_id", node.NodeID, "error", result.Error)
	} else if len(result.Data) > 0 {
		var redial struct {
			NewIP string `json:"new_ip"`
		}
		if json.Unmarshal(result.Data, &redial) == nil {
			reportedIP = strings.TrimSpace(redial.NewIP)
		}
	}
	if reportedIP != "" && reportedIP != oldIP {
		if err := s.store.UpdateNodeIPReplacement(node.NodeID, reportedIP, node.ProviderInstanceID); err != nil {
			s.logger.Warn("persist Agent-reported ADSL IP", "node_id", node.NodeID, "error", err)
		}
	}

	deadline := time.Now().Add(adslRedialWait)
	ticker := time.NewTicker(adslPollInterval)
	defer ticker.Stop()
	newIP := reportedIP
	for newIP == "" {
		if ctx.Err() != nil {
			break
		}
		current, getErr := s.store.GetNode(node.NodeID)
		if getErr == nil {
			candidate := strings.TrimSpace(current.RemoteIP)
			if candidate != "" && candidate != oldIP {
				newIP = candidate
				break
			}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
	if newIP == "" {
		return replaceIPResult{}, errors.New("ADSL redial completed but no new public IP was observed")
	}

	ddnsUpdated, warning := s.updateNodeDDNS(ctx, node.NodeID, newIP, reason)
	s.logger.Info("ADSL IP changed",
		"node_id", node.NodeID,
		"old_ip", oldIP,
		"new_ip", newIP,
		"reason", reason,
	)
	return replaceIPResult{
		NodeID:        node.NodeID,
		Provider:      "adsl",
		OldIP:         oldIP,
		NewIP:         newIP,
		NewInstanceID: node.ProviderInstanceID,
		DDNSDomain:    node.DDNSDomain,
		DDNSUpdated:   ddnsUpdated,
		Warning:       warning,
	}, nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
