package controlplane

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"nodehub/internal/store"
)

func (s *Server) quotaLoop(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.QuotaInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.enforceQuotas(ctx)
		}
	}
}

func (s *Server) enforceQuotas(ctx context.Context) {
	banned, err := s.store.BanExceededUsers(time.Now().UTC())
	if err != nil {
		s.logger.Error("enforce traffic quotas", "error", err)
		return
	}
	for _, user := range banned {
		s.logger.Warn("user traffic quota exceeded; banned",
			"user_email", user.UserEmail,
			"total_bytes", user.TotalBytes,
			"quota_bytes", user.QuotaBytes,
			"nodes", len(user.NodeIDs),
		)
		s.auditSystem("system:quota", "traffic_ban", "", map[string]any{
			"user_email":  user.UserEmail,
			"total_bytes": user.TotalBytes,
			"quota_bytes": user.QuotaBytes,
			"node_ids":    user.NodeIDs,
		})
		s.notifyTrafficBan(user.UserEmail, user.TotalBytes, user.QuotaBytes, len(user.NodeIDs))
		for _, nodeID := range user.NodeIDs {
			if ctx.Err() != nil {
				return
			}
			s.enforceBannedUser(ctx, nodeID, user.UserEmail)
		}
	}
}

// enforceBannedUser persists a config without the banned identity, then asks
// the Agent to apply it. If the node is offline the config is still saved and
// will be applied on reconnect.
func (s *Server) enforceBannedUser(ctx context.Context, nodeID, email string) {
	config, err := s.store.GetXrayConfig(nodeID)
	if err != nil {
		if !store.IsNotFound(err) {
			s.logger.Error("load Xray config for quota ban", "node_id", nodeID, "user_email", email, "error", err)
		}
		return
	}
	updated, changed, err := removeXrayIdentity(config.Config, email)
	if err != nil {
		s.logger.Error("remove banned user from Xray config", "node_id", nodeID, "user_email", email, "error", err)
		return
	}
	if !changed {
		s.logger.Warn("banned user not found in Xray config; cannot remove client", "node_id", nodeID, "user_email", email)
		return
	}
	if err := s.store.SaveXrayConfig(nodeID, updated); err != nil {
		s.logger.Error("save quota-enforced Xray config", "node_id", nodeID, "user_email", email, "error", err)
		return
	}
	if _, warning := s.syncXrayConfig(ctx, nodeID, updated, true); warning != "" {
		s.logger.Warn("apply quota-enforced Xray config", "node_id", nodeID, "user_email", email, "warning", warning)
	}
}

// removeXrayIdentity recursively removes client objects whose email, id,
// password, or user field matches the banned stats identity.
func removeXrayIdentity(configJSON, identity string) (string, bool, error) {
	decoder := json.NewDecoder(strings.NewReader(configJSON))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return configJSON, false, err
	}
	updated, changed := removeIdentityValue(root, identity)
	if !changed {
		return configJSON, false, nil
	}
	compact, err := json.Marshal(updated)
	if err != nil {
		return configJSON, false, err
	}
	return string(compact), true, nil
}

func removeIdentityValue(value any, identity string) (any, bool) {
	switch current := value.(type) {
	case map[string]any:
		changed := false
		for key, child := range current {
			if array, ok := child.([]any); ok {
				filtered := make([]any, 0, len(array))
				for _, item := range array {
					if object, ok := item.(map[string]any); ok && identityMatches(object, identity) {
						changed = true
						continue
					}
					updatedItem, itemChanged := removeIdentityValue(item, identity)
					if itemChanged {
						changed = true
					}
					filtered = append(filtered, updatedItem)
				}
				current[key] = filtered
				continue
			}
			updatedChild, childChanged := removeIdentityValue(child, identity)
			if childChanged {
				current[key] = updatedChild
				changed = true
			}
		}
		return current, changed
	case []any:
		changed := false
		filtered := make([]any, 0, len(current))
		for _, item := range current {
			if object, ok := item.(map[string]any); ok && identityMatches(object, identity) {
				changed = true
				continue
			}
			updatedItem, itemChanged := removeIdentityValue(item, identity)
			if itemChanged {
				changed = true
			}
			filtered = append(filtered, updatedItem)
		}
		return filtered, changed
	default:
		return value, false
	}
}

func identityMatches(object map[string]any, identity string) bool {
	for _, key := range []string{"email", "id", "password", "user"} {
		value, ok := object[key].(string)
		if !ok {
			continue
		}
		if key == "email" {
			if strings.EqualFold(value, identity) {
				return true
			}
			continue
		}
		if value == identity {
			return true
		}
	}
	return false
}
