// Package store owns the SQLite persistence layer for node state, deployment
// tokens, and port-forward desired state.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	stdlog "log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"nodehub/internal/protocol"
)

type Store struct {
	db *gorm.DB
}

func Open(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("database path must not be empty")
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		dir := filepath.Dir(path)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return nil, fmt.Errorf("create database directory: %w", err)
			}
		}
	}

	gormLogger := logger.New(
		stdlog.New(os.Stdout, "\r\n", stdlog.LstdFlags),
		logger.Config{
			SlowThreshold:             time.Second,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		},
	)
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: gormLogger,
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
	})
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sqlite handle: %w", err)
	}
	// SQLite has a single writer. Serializing through one connection avoids
	// SQLITE_BUSY under WebSocket/API concurrency and is plenty for this MVP.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	store := &Store{db: db}
	if err := store.configure(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	if err := store.Migrate(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		if err := os.Chmod(path, 0o600); err != nil {
			_ = sqlDB.Close()
			return nil, fmt.Errorf("restrict database permissions: %w", err)
		}
	}
	return store, nil
}

func (s *Store) configure() error {
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if err := s.db.Exec(pragma).Error; err != nil {
			return fmt.Errorf("configure sqlite (%s): %w", pragma, err)
		}
	}
	return nil
}

func (s *Store) Migrate() error {
	if err := s.db.AutoMigrate(&Node{}, &NodeTag{}, &AuditLog{}, &ProxyInbound{}, &Token{}, &PortForwardRule{}, &XrayConfig{}, &UserTraffic{}, &TrafficDaily{}, &TrafficUser{}, &APIKey{}, &AdminUser{}, &NodeDecommission{}, &CloudCredential{}, &Certificate{}, &NodeHealEvent{}, &NotificationSetting{}, &CloudOrphan{}, &TraefikSetting{}); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}
	return nil
}

// BackupTo creates a consistent online SQLite snapshot. VACUUM INTO reads
// through the same connection and includes committed WAL pages.
func (s *Store) BackupTo(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("backup path must not be empty")
	}
	if _, err := os.Stat(path); err == nil {
		return errors.New("backup destination already exists")
	}
	return s.db.Exec("VACUUM INTO ?", path).Error
}

func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func IsNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}

func (s *Store) CreateAuditLog(entry AuditLog) error {
	return s.db.Create(&entry).Error
}

func (s *Store) ListAuditLogs(page, pageSize int) ([]AuditLog, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 50
	}
	var total int64
	if err := s.db.Model(&AuditLog{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var entries []AuditLog
	err := s.db.
		Order("timestamp DESC").
		Order("id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&entries).Error
	return entries, total, err
}

func (s *Store) CountAdminUsers() (int64, error) {
	var count int64
	err := s.db.Model(&AdminUser{}).Count(&count).Error
	return count, err
}

func (s *Store) CreateAdminUser(username, passwordHash string) (AdminUser, error) {
	user := AdminUser{
		Username:     username,
		PasswordHash: passwordHash,
		Role:         RoleSuperAdmin,
		AllowedTags:  "[]",
	}
	err := s.db.Create(&user).Error
	return user, err
}

func (s *Store) CreateAdminUserWithRole(username, passwordHash, role, allowedTags string) (AdminUser, error) {
	user := AdminUser{
		Username:     username,
		PasswordHash: passwordHash,
		Role:         role,
		AllowedTags:  allowedTags,
	}
	err := s.db.Create(&user).Error
	return user, err
}

func (s *Store) ListAdminUsers() ([]AdminUser, error) {
	var users []AdminUser
	err := s.db.Order("id ASC").Find(&users).Error
	return users, err
}

func (s *Store) UpdateAdminUserAccess(id uint, role, allowedTags string) error {
	result := s.db.Model(&AdminUser{}).Where("id = ?", id).Updates(map[string]any{
		"role": role, "allowed_tags": allowedTags,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *Store) DeleteAdminUser(id uint) error {
	result := s.db.Delete(&AdminUser{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *Store) GetAdminUserByUsername(username string) (AdminUser, error) {
	var user AdminUser
	err := s.db.Where("username = ?", username).First(&user).Error
	return user, err
}

func (s *Store) GetAdminUserByID(id uint) (AdminUser, error) {
	var user AdminUser
	err := s.db.First(&user, id).Error
	return user, err
}

func (s *Store) SetAdminTOTP(id uint, secret string, enabled bool) error {
	return s.db.Model(&AdminUser{}).Where("id = ?", id).
		Updates(map[string]any{"totp_secret": secret, "totp_enabled": enabled}).Error
}

func (s *Store) UpdateAdminPassword(id uint, passwordHash string) error {
	result := s.db.Model(&AdminUser{}).Where("id = ?", id).Update("password_hash", passwordHash)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *Store) MarkAllNodesOffline() error {
	return s.db.Model(&Node{}).
		Where("status <> ?", NodeStatusOffline).
		Update("status", NodeStatusOffline).Error
}

func (s *Store) CreateProvisioningNode(nodeID, name, address string) error {
	if err := s.ClearNodeDecommission(nodeID); err != nil {
		return err
	}
	node := Node{
		NodeID:  nodeID,
		Name:    name,
		Address: address,
		Status:  NodeStatusProvisioning,
	}
	return s.db.Create(&node).Error
}

func (s *Store) MarkNodeOnline(info protocol.AgentInfo, remoteIP string, at time.Time) error {
	plugins, err := json.Marshal(info.Plugins)
	if err != nil {
		return fmt.Errorf("encode plugin list: %w", err)
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var node Node
		err := tx.Where("node_id = ?", info.ID).First(&node).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			node = Node{
				NodeID:  info.ID,
				Address: remoteIP,
			}
		} else if err != nil {
			return err
		}

		if !node.NameManual {
			if name := strings.TrimSpace(info.Hostname); name != "" {
				node.Name = name
			} else if node.Name == "" {
				node.Name = info.ID
			}
		}
		if node.Address == "" {
			node.Address = remoteIP
		}
		node.RemoteIP = remoteIP
		node.Status = NodeStatusOnline
		node.OS = info.OS
		node.Arch = info.Arch
		node.AgentVersion = info.Version
		node.Plugins = string(plugins)
		node.ConnectedAt = &at
		node.LastSeen = &at
		// An authenticated Agent reconnect is a fresh connectivity baseline.
		node.ProbeFailures = 0
		node.GFWFailures = 0
		node.GFWBlockedAt = nil
		return tx.Save(&node).Error
	})
}

func (s *Store) MarkNodeOffline(nodeID string, at time.Time) error {
	return s.db.Model(&Node{}).
		Where("node_id = ?", nodeID).
		Updates(map[string]any{
			"status":    NodeStatusOffline,
			"last_seen": at,
		}).Error
}

func (s *Store) TouchNodes(nodeIDs []string, at time.Time) error {
	if len(nodeIDs) == 0 {
		return nil
	}
	return s.db.Model(&Node{}).
		Where("node_id IN ? AND status = ?", nodeIDs, NodeStatusOnline).
		Update("last_seen", at).Error
}

func (s *Store) ListNodes() ([]Node, error) {
	var nodes []Node
	err := s.db.
		Order("CASE WHEN status = 'online' THEN 0 WHEN status = 'provisioning' THEN 1 ELSE 2 END").
		Order("last_seen DESC").
		Order("node_id ASC").
		Find(&nodes).Error
	return nodes, err
}

type NodePage struct {
	Nodes []Node
	Total int64
	Page  int
	Limit int
}

func (s *Store) ListNodesPage(page, limit int, search, status, tag string) (NodePage, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	query := s.db.Model(&Node{})
	if value := strings.TrimSpace(search); value != "" {
		pattern := "%" + strings.ToLower(value) + "%"
		query = query.Where(
			"LOWER(node_id) LIKE ? OR LOWER(name) LIKE ? OR LOWER(address) LIKE ?",
			pattern, pattern, pattern,
		)
	}
	if value := strings.TrimSpace(status); value != "" {
		query = query.Where("status = ?", value)
	}
	if value := strings.TrimSpace(tag); value != "" {
		query = query.Where("node_id IN (?)", s.db.Model(&NodeTag{}).Select("node_id").Where("tag_name = ?", value))
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return NodePage{}, err
	}
	var nodes []Node
	err := query.
		Order("CASE WHEN status = 'online' THEN 0 WHEN status = 'provisioning' THEN 1 ELSE 2 END").
		Order("last_seen DESC").
		Order("node_id ASC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&nodes).Error
	return NodePage{Nodes: nodes, Total: total, Page: page, Limit: limit}, err
}

func (s *Store) SaveCertificate(certificate *Certificate) error {
	return s.db.Save(certificate).Error
}

func (s *Store) ListCertificates() ([]Certificate, error) {
	var certificates []Certificate
	err := s.db.Order("domain ASC").Find(&certificates).Error
	return certificates, err
}

func (s *Store) GetCertificate(id uint) (Certificate, error) {
	var certificate Certificate
	err := s.db.First(&certificate, id).Error
	return certificate, err
}

func (s *Store) DeleteCertificate(id uint) error {
	return s.db.Delete(&Certificate{}, id).Error
}

func (s *Store) RecordCloudOrphan(provider, instanceID, reason string, at time.Time) error {
	var orphan CloudOrphan
	err := s.db.Where("provider = ? AND instance_id = ?", provider, instanceID).First(&orphan).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s.db.Create(&CloudOrphan{
			Provider: provider, InstanceID: instanceID, Reason: reason, CreatedAt: at, UpdatedAt: at,
		}).Error
	}
	if err != nil {
		return err
	}
	return s.db.Model(&orphan).Updates(map[string]any{
		"reason": reason, "updated_at": at, "resolved_at": nil, "attempts": 0, "last_error": "",
	}).Error
}

func (s *Store) ListCloudOrphans(limit int) ([]CloudOrphan, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows []CloudOrphan
	err := s.db.Where("resolved_at IS NULL AND attempts < 12").
		Order("updated_at ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

func (s *Store) MarkCloudOrphanResult(id uint, errText string, resolved bool, at time.Time) error {
	updates := map[string]any{
		"attempts": gorm.Expr("attempts + 1"), "last_error": errText, "updated_at": at,
	}
	if resolved {
		updates["resolved_at"] = at
	}
	return s.db.Model(&CloudOrphan{}).Where("id = ?", id).Updates(updates).Error
}

func (s *Store) GetTraefikSetting() (TraefikSetting, error) {
	var setting TraefikSetting
	err := s.db.First(&setting, 1).Error
	return setting, err
}

func (s *Store) SaveTraefikSetting(setting TraefikSetting) error {
	setting.ID = 1
	return s.db.Save(&setting).Error
}

func (s *Store) GetNotificationSetting() (NotificationSetting, error) {
	var setting NotificationSetting
	err := s.db.First(&setting, 1).Error
	return setting, err
}

func (s *Store) SaveNotificationSetting(setting NotificationSetting) error {
	setting.ID = 1
	return s.db.Save(&setting).Error
}

func (s *Store) SetCloudCredential(provider, apiKey string, at time.Time) error {
	return s.db.Save(&CloudCredential{Provider: provider, APIKey: apiKey, UpdatedAt: at}).Error
}

func (s *Store) GetCloudCredential(provider string) (CloudCredential, error) {
	var credential CloudCredential
	err := s.db.Where("provider = ?", provider).First(&credential).Error
	return credential, err
}

func (s *Store) ListCloudCredentials() ([]CloudCredential, error) {
	var credentials []CloudCredential
	err := s.db.Order("provider ASC").Find(&credentials).Error
	return credentials, err
}

func (s *Store) DeleteCloudCredential(provider string) error {
	return s.db.Where("provider = ?", provider).Delete(&CloudCredential{}).Error
}

func (s *Store) CountBannedUsers() (int64, error) {
	var count int64
	err := s.db.Model(&TrafficUser{}).Where("is_banned = ?", true).Count(&count).Error
	return count, err
}

func (s *Store) CountNodesByStatus() (map[string]int64, error) {
	type row struct {
		Status string
		Count  int64
	}
	var rows []row
	if err := s.db.Model(&Node{}).Select("status, COUNT(*) AS count").Group("status").Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]int64, len(rows))
	for _, item := range rows {
		result[item.Status] = item.Count
	}
	return result, nil
}

func (s *Store) MonthlyTraffic(monthStart time.Time) (upload, download uint64, err error) {
	type totals struct {
		Upload   uint64
		Download uint64
	}
	var value totals
	err = s.db.Model(&TrafficDaily{}).
		Select("COALESCE(SUM(upload_bytes), 0) AS upload, COALESCE(SUM(download_bytes), 0) AS download").
		Where("day >= ?", monthStart.UTC().Format("2006-01-02")).
		Scan(&value).Error
	return value.Upload, value.Download, err
}

func (s *Store) GetNode(nodeID string) (Node, error) {
	var node Node
	err := s.db.Where("node_id = ?", nodeID).First(&node).Error
	return node, err
}

func (s *Store) UpdateNodeIdentity(nodeID, name, address string) error {
	result := s.db.Model(&Node{}).Where("node_id = ?", nodeID).Updates(map[string]any{
		"name":        name,
		"address":     address,
		"name_manual": true,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *Store) UpdateNodeCloudSettings(nodeID, provider, instanceID, ddnsDomain, ddnsZoneID, ddnsRecordID string, autoHeal bool) error {
	result := s.db.Model(&Node{}).Where("node_id = ?", nodeID).Updates(map[string]any{
		"cloud_provider":       strings.TrimSpace(provider),
		"provider_instance_id": strings.TrimSpace(instanceID),
		"ddns_domain":          strings.TrimSpace(ddnsDomain),
		"ddns_zone_id":         strings.TrimSpace(ddnsZoneID),
		"ddns_record_id":       strings.TrimSpace(ddnsRecordID),
		"auto_heal":            autoHeal,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// TryClaimAutoHeal is the legacy automatic-heal entry point. It now enforces the
// persistent 24h hard limit in addition to the short cooldown.
func (s *Store) TryClaimAutoHeal(nodeID string, now time.Time, cooldown time.Duration) (bool, error) {
	return s.TryClaimNodeReplacement(nodeID, "auto_heal", "auto_heal", now, cooldown, 2)
}

// TryClaimNodeReplacement atomically reserves one replacement attempt. The
// ledger row is written before the cloud API call, so a process crash cannot
// bypass the quota. A 24h cap of two attempts is the production default.
func (s *Store) CountNodeHealEvents(nodeID string, since time.Time) (int64, error) {
	var count int64
	err := s.db.Model(&NodeHealEvent{}).
		Where("node_id = ? AND created_at >= ?", nodeID, since).
		Count(&count).Error
	return count, err
}

func (s *Store) TryClaimNodeReplacement(nodeID, action, reason string, now time.Time, cooldown time.Duration, dailyLimit int) (bool, error) {
	if dailyLimit <= 0 {
		dailyLimit = 2
	}
	claimed := false
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var node Node
		if err := tx.Where("node_id = ?", nodeID).First(&node).Error; err != nil {
			return err
		}
		if action == "auto_heal" {
			provider := strings.ToLower(strings.TrimSpace(node.CloudProvider))
			if !node.AutoHeal || provider == "" {
				return nil
			}
			if provider != "adsl" && strings.TrimSpace(node.ProviderInstanceID) == "" {
				return nil
			}
			if node.LastAutoHealAt != nil && now.Sub(*node.LastAutoHealAt) < cooldown {
				return nil
			}
		}
		var count int64
		query := tx.Model(&NodeHealEvent{}).
			Where("node_id = ? AND created_at >= ?", nodeID, now.Add(-24*time.Hour))
		if action == "dd_reinstall" {
			query = query.Where("action = ?", action)
		} else {
			// IP replacement and automatic rebuild share one budget; DD has an
			// independent budget so one category cannot exhaust the other.
			query = query.Where("action IN ?", []string{"auto_heal", "manual_replace"})
		}
		if err := query.Count(&count).Error; err != nil {
			return err
		}
		if count >= int64(dailyLimit) {
			return nil
		}
		if err := tx.Create(&NodeHealEvent{NodeID: nodeID, Action: action, Reason: reason, CreatedAt: now}).Error; err != nil {
			return err
		}
		if action == "auto_heal" {
			if err := tx.Model(&Node{}).Where("node_id = ?", nodeID).
				Update("last_auto_heal_at", now).Error; err != nil {
				return err
			}
		}
		claimed = true
		return nil
	})
	return claimed, err
}

func (s *Store) UpdateNodeIPReplacement(nodeID, newIP, newInstanceID string) error {
	updates := map[string]any{"remote_ip": strings.TrimSpace(newIP)}
	if strings.TrimSpace(newInstanceID) != "" {
		updates["provider_instance_id"] = strings.TrimSpace(newInstanceID)
	}
	result := s.db.Model(&Node{}).Where("node_id = ?", nodeID).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

var tagPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)

func NormalizeTags(tags []string) ([]string, error) {
	if len(tags) > 32 {
		return nil, errors.New("a node may have at most 32 tags")
	}
	seen := make(map[string]struct{}, len(tags))
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if !tagPattern.MatchString(tag) {
			return nil, fmt.Errorf("invalid tag %q", tag)
		}
		if _, exists := seen[tag]; exists {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	return result, nil
}

func (s *Store) SetNodeTags(nodeID string, tags []string) error {
	normalized, err := NormalizeTags(tags)
	if err != nil {
		return err
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("node_id = ?", nodeID).Delete(&NodeTag{}).Error; err != nil {
			return err
		}
		for _, tag := range normalized {
			if err := tx.Create(&NodeTag{NodeID: nodeID, TagName: tag}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) ListNodeTags(nodeID string) ([]string, error) {
	var tags []string
	err := s.db.Model(&NodeTag{}).Where("node_id = ?", nodeID).Order("tag_name ASC").Pluck("tag_name", &tags).Error
	return tags, err
}

func (s *Store) ListAllNodeTags() (map[string][]string, error) {
	var rows []NodeTag
	if err := s.db.Order("tag_name ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string][]string)
	for _, row := range rows {
		result[row.NodeID] = append(result[row.NodeID], row.TagName)
	}
	return result, nil
}

func (s *Store) ListTagCounts() ([]TagCount, error) {
	var counts []TagCount
	err := s.db.Model(&NodeTag{}).
		Select("tag_name, COUNT(*) AS count").
		Group("tag_name").
		Order("tag_name ASC").
		Scan(&counts).Error
	return counts, err
}

func (s *Store) ListOnlineNodes() ([]Node, error) {
	var nodes []Node
	err := s.db.Where("status = ?", NodeStatusOnline).Order("node_id ASC").Find(&nodes).Error
	return nodes, err
}

func (s *Store) ListOnlineNodesByIDs(ids []string) ([]Node, error) {
	if len(ids) == 0 {
		return []Node{}, nil
	}
	var nodes []Node
	err := s.db.Where("status = ? AND node_id IN ?", NodeStatusOnline, ids).Order("node_id ASC").Find(&nodes).Error
	return nodes, err
}

func (s *Store) ListOnlineNodesWithAllTags(tags []string) ([]Node, error) {
	normalized, err := NormalizeTags(tags)
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return []Node{}, nil
	}
	subquery := s.db.Model(&NodeTag{}).
		Select("node_id").
		Where("tag_name IN ?", normalized).
		Group("node_id").
		Having("COUNT(DISTINCT tag_name) = ?", len(normalized))
	var nodes []Node
	err = s.db.Where("status = ? AND node_id IN (?)", NodeStatusOnline, subquery).
		Order("node_id ASC").
		Find(&nodes).Error
	return nodes, err
}

func (s *Store) ListProbeCandidates() ([]Node, error) {
	var nodes []Node
	err := s.db.
		Where("status IN ?", []string{NodeStatusOnline, NodeStatusNetworkError, NodeStatusGFWBlocked}).
		Order("node_id ASC").
		Find(&nodes).Error
	return nodes, err
}

// RecordProbeFailure returns the resulting node status. The caller serializes
// this with WebSocket online/offline transitions.
func (s *Store) RecordProbeFailure(nodeID string, threshold int, at time.Time) (string, bool, error) {
	var resulting string
	newlyMarked := false
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var node Node
		if err := tx.Where("node_id = ?", nodeID).First(&node).Error; err != nil {
			return err
		}
		if node.Status != NodeStatusOnline && node.Status != NodeStatusNetworkError && node.Status != NodeStatusGFWBlocked {
			resulting = node.Status
			return nil
		}
		node.ProbeFailures++
		updates := map[string]any{"probe_failures": node.ProbeFailures}
		if (node.Status == NodeStatusOnline || node.Status == NodeStatusGFWBlocked) && node.ProbeFailures >= threshold {
			node.Status = NodeStatusNetworkError
			node.NetworkErrorAt = &at
			updates["status"] = NodeStatusNetworkError
			updates["network_error_at"] = at
			updates["gfw_blocked_at"] = nil
			newlyMarked = true
		}
		if err := tx.Model(&Node{}).Where("node_id = ?", nodeID).Updates(updates).Error; err != nil {
			return err
		}
		resulting = node.Status
		return nil
	})
	return resulting, newlyMarked, err
}

func (s *Store) RecordProbeSuccess(nodeID string, restoreOnline bool) error {
	updates := map[string]any{
		"probe_failures":   0,
		"gfw_failures":     0,
		"gfw_blocked_at":   nil,
		"network_error_at": nil,
	}
	query := s.db.Model(&Node{}).Where("node_id = ?", nodeID)
	if restoreOnline {
		query = query.Where("status IN ?", []string{NodeStatusNetworkError, NodeStatusGFWBlocked})
		updates["status"] = NodeStatusOnline
	}
	return query.Updates(updates).Error
}

// RecordGFWBlocked is called only after the overseas TCP probe succeeds but
// independent domestic probes report a blocked TCP path. It requires a
// consecutive threshold to avoid turning a single flaky domestic probe into an
// IP replacement.
func (s *Store) RecordGFWBlocked(nodeID string, threshold int, at time.Time) (string, bool, error) {
	if threshold <= 0 {
		threshold = 2
	}
	var resulting string
	newlyMarked := false
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var node Node
		if err := tx.Where("node_id = ?", nodeID).First(&node).Error; err != nil {
			return err
		}
		if node.Status == NodeStatusProvisioning || node.Status == NodeStatusOffline {
			resulting = node.Status
			return nil
		}
		node.GFWFailures++
		node.ProbeFailures = 0
		updates := map[string]any{
			"gfw_failures":   node.GFWFailures,
			"probe_failures": 0,
		}
		switch {
		case node.GFWFailures >= threshold && node.Status != NodeStatusGFWBlocked:
			node.Status = NodeStatusGFWBlocked
			node.GFWBlockedAt = &at
			updates["status"] = NodeStatusGFWBlocked
			updates["gfw_blocked_at"] = at
			updates["network_error_at"] = nil
			newlyMarked = true
		case node.Status == NodeStatusNetworkError:
			// The overseas TCP path is healthy, so the node is no longer in a
			// generic network-error state while domestic evidence accumulates.
			node.Status = NodeStatusOnline
			updates["status"] = NodeStatusOnline
			updates["network_error_at"] = nil
		}
		if err := tx.Model(&Node{}).Where("node_id = ?", nodeID).Updates(updates).Error; err != nil {
			return err
		}
		resulting = node.Status
		return nil
	})
	return resulting, newlyMarked, err
}

func (s *Store) DecommissionNode(nodeID string, at time.Time) error {
	return s.db.Save(&NodeDecommission{NodeID: nodeID, DecommissionedAt: at}).Error
}

func (s *Store) IsNodeDecommissioned(nodeID string) (bool, error) {
	var count int64
	err := s.db.Model(&NodeDecommission{}).Where("node_id = ?", nodeID).Count(&count).Error
	return count > 0, err
}

func (s *Store) ClearNodeDecommission(nodeID string) error {
	return s.db.Where("node_id = ?", nodeID).Delete(&NodeDecommission{}).Error
}

func (s *Store) DeleteNode(nodeID string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("node_id = ?", nodeID).Delete(&PortForwardRule{}).Error; err != nil {
			return err
		}
		if err := tx.Where("node_id = ?", nodeID).Delete(&ProxyInbound{}).Error; err != nil {
			return err
		}
		if err := tx.Where("node_id = ?", nodeID).Delete(&XrayConfig{}).Error; err != nil {
			return err
		}
		if err := tx.Where("node_id = ?", nodeID).Delete(&UserTraffic{}).Error; err != nil {
			return err
		}
		if err := tx.Where("node_id = ?", nodeID).Delete(&NodeTag{}).Error; err != nil {
			return err
		}
		// traffic_users is keyed by global email; quota rows intentionally survive
		// individual node deletion.
		if err := tx.Where("node_id = ?", nodeID).Delete(&Token{}).Error; err != nil {
			return err
		}
		return tx.Where("node_id = ?", nodeID).Delete(&Node{}).Error
	})
}

func (s *Store) ReplaceToken(nodeID, tokenHash string, at time.Time) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("node_id = ?", nodeID).Delete(&Token{}).Error; err != nil {
			return err
		}
		return tx.Create(&Token{
			NodeID:    nodeID,
			TokenHash: tokenHash,
			CreatedAt: at,
		}).Error
	})
}

func (s *Store) ListTokens() ([]Token, error) {
	var tokens []Token
	err := s.db.Find(&tokens).Error
	return tokens, err
}

func (s *Store) MarkTokenUsed(tokenHash string, at time.Time) error {
	return s.db.Model(&Token{}).
		Where("token_hash = ? AND first_used_at IS NULL", tokenHash).
		Update("first_used_at", at).Error
}

func (s *Store) DeleteToken(tokenHash string) error {
	return s.db.Where("token_hash = ?", tokenHash).Delete(&Token{}).Error
}

func (s *Store) ListPortForwardRules(nodeID string) ([]PortForwardRule, error) {
	var rules []PortForwardRule
	err := s.db.
		Where("node_id = ?", nodeID).
		Order("listen_port ASC").
		Find(&rules).Error
	return rules, err
}

func (s *Store) GetPortForwardRule(nodeID, ruleID string) (PortForwardRule, error) {
	var rule PortForwardRule
	err := s.db.Where("node_id = ? AND rule_id = ?", nodeID, ruleID).First(&rule).Error
	return rule, err
}

func (s *Store) CreatePortForwardRule(rule *PortForwardRule) error {
	return s.db.Create(rule).Error
}

func (s *Store) SavePortForwardRule(rule *PortForwardRule) error {
	return s.db.Save(rule).Error
}

func (s *Store) DeletePortForwardRule(nodeID, ruleID string) error {
	result := s.db.Where("node_id = ? AND rule_id = ?", nodeID, ruleID).Delete(&PortForwardRule{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ApplyTrafficUpdate converts cumulative Xray counters into accumulated totals.
// Raw counters are stored so dropped WebSocket frames cannot lose traffic:
// the next cumulative sample catches up.
func (s *Store) ApplyTrafficUpdate(nodeID string, update protocol.TrafficUpdate, at time.Time) error {
	sessionID := truncate(update.SessionID, 64)
	unique := make(map[string]protocol.TrafficUserStat, len(update.Users))
	for _, stat := range update.Users {
		email := strings.TrimSpace(stat.Email)
		if email == "" || len(email) > 255 {
			continue
		}
		unique[email] = stat
		if len(unique) >= 1000 {
			break
		}
	}
	if len(unique) == 0 {
		return nil
	}

	var dayUpload, dayDownload uint64
	day := at.UTC().Format("2006-01-02")
	err := s.db.Transaction(func(tx *gorm.DB) error {
		for email, stat := range unique {
			var row UserTraffic
			err := tx.Where("node_id = ? AND user_email = ?", nodeID, email).First(&row).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				row = UserTraffic{
					NodeID:           nodeID,
					UserEmail:        email,
					UploadBytes:      stat.UploadBytes,
					DownloadBytes:    stat.DownloadBytes,
					RawUploadBytes:   stat.UploadBytes,
					RawDownloadBytes: stat.DownloadBytes,
					SessionID:        sessionID,
					LastUpdated:      at,
				}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
				dayUpload = saturatingAdd(dayUpload, stat.UploadBytes)
				dayDownload = saturatingAdd(dayDownload, stat.DownloadBytes)
				continue
			}
			if err != nil {
				return err
			}

			var uploadDelta, downloadDelta uint64
			if row.SessionID != sessionID {
				uploadDelta = stat.UploadBytes
				downloadDelta = stat.DownloadBytes
			} else {
				uploadDelta = counterDelta(row.RawUploadBytes, stat.UploadBytes)
				downloadDelta = counterDelta(row.RawDownloadBytes, stat.DownloadBytes)
			}
			row.UploadBytes = saturatingAdd(row.UploadBytes, uploadDelta)
			row.DownloadBytes = saturatingAdd(row.DownloadBytes, downloadDelta)
			row.RawUploadBytes = stat.UploadBytes
			row.RawDownloadBytes = stat.DownloadBytes
			row.SessionID = sessionID
			row.LastUpdated = at
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
			dayUpload = saturatingAdd(dayUpload, uploadDelta)
			dayDownload = saturatingAdd(dayDownload, downloadDelta)
		}
		if dayUpload == 0 && dayDownload == 0 {
			return nil
		}
		var daily TrafficDaily
		err := tx.Where("day = ?", day).First(&daily).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&TrafficDaily{
				Day:           day,
				UploadBytes:   dayUpload,
				DownloadBytes: dayDownload,
				UpdatedAt:     at,
			}).Error
		}
		if err != nil {
			return err
		}
		return tx.Model(&TrafficDaily{}).Where("day = ?", day).Updates(map[string]any{
			"upload_bytes":   gorm.Expr("upload_bytes + ?", dayUpload),
			"download_bytes": gorm.Expr("download_bytes + ?", dayDownload),
			"updated_at":     at,
		}).Error
	})
	return err
}

func (s *Store) ListUserTraffic(nodeID string) ([]UserTraffic, error) {
	var rows []UserTraffic
	err := s.db.Where("node_id = ?", nodeID).Order("user_email ASC").Find(&rows).Error
	return rows, err
}

func (s *Store) SetUserQuota(email string, quotaBytes uint64) error {
	email = strings.TrimSpace(email)
	if email == "" || len(email) > 255 {
		return errors.New("invalid user email")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var user TrafficUser
		err := tx.Where("user_email = ?", email).First(&user).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&TrafficUser{UserEmail: email, QuotaBytes: quotaBytes}).Error
		}
		if err != nil {
			return err
		}
		return tx.Model(&TrafficUser{}).
			Where("user_email = ?", email).
			Update("quota_bytes", quotaBytes).Error
	})
}

func (s *Store) AllocateTraffic(email string, quotaBytes uint64, deviceLimit int, nodeIDs []string, resetUsage bool) (TrafficUser, error) {
	email = strings.TrimSpace(email)
	if email == "" || len(email) > 255 {
		return TrafficUser{}, errors.New("invalid user email")
	}
	if deviceLimit < 0 {
		return TrafficUser{}, errors.New("device limit must not be negative")
	}
	var user TrafficUser
	err := s.db.Transaction(func(tx *gorm.DB) error {
		err := tx.Where("user_email = ?", email).First(&user).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			user = TrafficUser{UserEmail: email, QuotaBytes: quotaBytes, DeviceLimit: deviceLimit}
			return tx.Create(&user).Error
		}
		if err != nil {
			return err
		}
		updates := map[string]any{
			"quota_bytes":  quotaBytes,
			"device_limit": deviceLimit,
			"is_banned":    false,
			"banned_at":    nil,
		}
		if err := tx.Model(&TrafficUser{}).Where("user_email = ?", email).Updates(updates).Error; err != nil {
			return err
		}
		if resetUsage {
			query := tx.Where("user_email = ?", email)
			if len(nodeIDs) > 0 {
				query = query.Where("node_id IN ?", nodeIDs)
			}
			if err := query.Delete(&UserTraffic{}).Error; err != nil {
				return err
			}
		}
		return tx.Where("user_email = ?", email).First(&user).Error
	})
	return user, err
}

func (s *Store) BanTrafficUser(email string, at time.Time) ([]string, error) {
	email = strings.TrimSpace(email)
	if email == "" || len(email) > 255 {
		return nil, errors.New("invalid user email")
	}
	var nodeIDs []string
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&UserTraffic{}).Where("user_email = ?", email).
			Distinct().Pluck("node_id", &nodeIDs).Error; err != nil {
			return err
		}
		var user TrafficUser
		err := tx.Where("user_email = ?", email).First(&user).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&TrafficUser{
				UserEmail: email, IsBanned: true, BannedAt: &at,
			}).Error
		}
		if err != nil {
			return err
		}
		return tx.Model(&TrafficUser{}).Where("user_email = ?", email).
			Updates(map[string]any{"is_banned": true, "banned_at": at}).Error
	})
	return nodeIDs, err
}

func (s *Store) UnbanTrafficUser(email string) error {
	result := s.db.Model(&TrafficUser{}).Where("user_email = ?", email).
		Updates(map[string]any{"is_banned": false, "banned_at": nil})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *Store) ListTrafficQuotas() ([]TrafficUser, error) {
	var users []TrafficUser
	err := s.db.Order("user_email ASC").Find(&users).Error
	return users, err
}

func (s *Store) CreateAPIKey(key *APIKey) error {
	return s.db.Create(key).Error
}

func (s *Store) ListAPIKeys() ([]APIKey, error) {
	var keys []APIKey
	err := s.db.Order("created_at DESC").Find(&keys).Error
	return keys, err
}

func (s *Store) GetAPIKeyByHash(hash string) (APIKey, error) {
	var key APIKey
	err := s.db.Where("key_hash = ? AND revoked = ?", hash, false).First(&key).Error
	return key, err
}

func (s *Store) RevokeAPIKey(id uint) error {
	result := s.db.Model(&APIKey{}).Where("id = ? AND revoked = ?", id, false).Update("revoked", true)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *Store) TouchAPIKey(id uint, at time.Time) error {
	return s.db.Model(&APIKey{}).Where("id = ?", id).Update("last_used_at", at).Error
}

type TrafficUserSummary struct {
	UserEmail     string `json:"user_email"`
	UploadBytes   uint64 `json:"upload_bytes"`
	DownloadBytes uint64 `json:"download_bytes"`
	TotalBytes    uint64 `json:"total_bytes"`
	NodeCount     int    `json:"node_count"`
	LastUpdated   string `json:"last_updated"`
	QuotaBytes    uint64 `json:"quota_bytes"`
	DeviceLimit   int    `json:"device_limit"`
	IsBanned      bool   `json:"is_banned"`
}

// SeedDemoData creates a small deterministic fleet for the read-only demo.
// It is idempotent and must never touch an existing production node.
func (s *Store) SeedDemoData() error {
	now := time.Now().UTC()
	demoNodes := []struct {
		id, name, address, os, region string
	}{
		{"demo-hk", "HK Edge 01", "hk.demo.nodehub.local", "Debian 12", "region:hk"},
		{"demo-sg", "SG Edge 02", "sg.demo.nodehub.local", "Debian 12", "region:sg"},
		{"demo-us", "US Edge 03", "us.demo.nodehub.local", "Ubuntu 24.04", "region:us"},
	}
	for _, item := range demoNodes {
		if _, err := s.GetNode(item.id); err != nil {
			if err := s.CreateProvisioningNode(item.id, item.name, item.address); err != nil {
				return err
			}
		}
		// Demo nodes are synthetic: make them online again after a restart
		// instead of skipping existing rows (shutdown marks every node offline).
		if err := s.MarkNodeOnline(protocol.AgentInfo{ID: item.id, Hostname: item.name, OS: item.os, Version: "1.0.0-beta"}, item.address, now); err != nil {
			return err
		}
		_ = s.UpdateNodeIdentity(item.id, item.name, item.address)
		_ = s.SetNodeTags(item.id, []string{item.region, "type:relay"})
	}
	count, err := s.CountProxyInbounds("demo-hk")
	if err != nil {
		return err
	}
	if count == 0 {
		settings := `{"clients":[{"id":"11111111-1111-4111-8111-111111111111","email":"demo@example.com","flow":""}]}`
		for _, inbound := range []ProxyInbound{
			{NodeID: "demo-hk", Remark: "HK VLESS TCP", Port: 23001, Protocol: "vless", Network: "tcp", Security: "none", SettingsJSON: settings},
			{NodeID: "demo-sg", Remark: "SG VLESS WS", Port: 23002, Protocol: "vless", Network: "ws", Security: "tls", SettingsJSON: settings},
		} {
			inbound := inbound
			if err := s.CreateProxyInbound(&inbound); err != nil {
				return err
			}
		}
	}
	var dailyCount int64
	if err := s.db.Model(&TrafficDaily{}).Count(&dailyCount).Error; err != nil {
		return err
	}
	if dailyCount == 0 {
		for offset := 29; offset >= 0; offset-- {
			day := now.AddDate(0, 0, -offset)
			upload := uint64(2_000_000_000 + (29-offset)*180_000_000)
			download := upload * 4
			row := TrafficDaily{Day: day.Format("2006-01-02"), UploadBytes: upload, DownloadBytes: download, UpdatedAt: now}
			if err := s.db.Create(&row).Error; err != nil {
				return err
			}
		}
	}
	users := []struct {
		email  string
		quota  uint64
		upload uint64
	}{
		{"alice@example.com", 100 << 30, 12 << 30},
		{"bob@example.com", 50 << 30, 9 << 30},
		{"carol@example.com", 0, 4 << 30},
	}
	for index, user := range users {
		if err := s.db.Where("user_email = ?", user.email).FirstOrCreate(&TrafficUser{
			UserEmail: user.email, QuotaBytes: user.quota, DeviceLimit: 3,
		}).Error; err != nil {
			return err
		}
		for nodeIndex, node := range demoNodes {
			if nodeIndex > index%2 {
				continue
			}
			traffic := UserTraffic{
				NodeID: node.id, UserEmail: user.email,
				UploadBytes: user.upload / 4, DownloadBytes: user.upload,
				RawUploadBytes: user.upload / 4, RawDownloadBytes: user.upload,
				LastUpdated: now,
			}
			if err := s.db.Where("node_id = ? AND user_email = ?", node.id, user.email).
				FirstOrCreate(&traffic).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) CountProxyInbounds(nodeID string) (int64, error) {
	var count int64
	err := s.db.Model(&ProxyInbound{}).Where("node_id = ?", nodeID).Count(&count).Error
	return count, err
}

func (s *Store) ListTrafficDaily(days int) ([]TrafficDaily, error) {
	if days < 1 {
		days = 30
	}
	if days > 365 {
		days = 365
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days+1).Format("2006-01-02")
	var rows []TrafficDaily
	err := s.db.Where("day >= ?", cutoff).Order("day ASC").Find(&rows).Error
	return rows, err
}

func (s *Store) ListTrafficUserSummaries() ([]TrafficUserSummary, error) {
	var summaries []TrafficUserSummary
	err := s.db.Raw(`
		SELECT
			ut.user_email,
			COALESCE(SUM(ut.upload_bytes), 0) AS upload_bytes,
			COALESCE(SUM(ut.download_bytes), 0) AS download_bytes,
			COALESCE(SUM(ut.upload_bytes + ut.download_bytes), 0) AS total_bytes,
			COUNT(DISTINCT ut.node_id) AS node_count,
			MAX(ut.last_updated) AS last_updated,
			COALESCE(tu.quota_bytes, 0) AS quota_bytes,
			COALESCE(tu.device_limit, 0) AS device_limit,
			COALESCE(tu.is_banned, 0) AS is_banned
		FROM user_traffic ut
		LEFT JOIN traffic_users tu ON tu.user_email = ut.user_email
		GROUP BY ut.user_email, tu.quota_bytes, tu.device_limit, tu.is_banned
		ORDER BY total_bytes DESC, ut.user_email ASC
	`).Scan(&summaries).Error
	return summaries, err
}

type BannedUser struct {
	UserEmail  string
	TotalBytes uint64
	QuotaBytes uint64
	NodeIDs    []string
}

func (s *Store) BanExceededUsers(at time.Time) ([]BannedUser, error) {
	summaries, err := s.ListTrafficUserSummaries()
	if err != nil {
		return nil, err
	}
	var banned []BannedUser
	for _, summary := range summaries {
		if summary.QuotaBytes == 0 || summary.TotalBytes <= summary.QuotaBytes || summary.IsBanned {
			continue
		}
		result := s.db.Model(&TrafficUser{}).
			Where("user_email = ? AND is_banned = ?", summary.UserEmail, false).
			Updates(map[string]any{"is_banned": true, "banned_at": at})
		if result.Error != nil {
			return banned, result.Error
		}
		if result.RowsAffected == 0 {
			continue
		}
		var nodeIDs []string
		if err := s.db.Model(&UserTraffic{}).
			Where("user_email = ?", summary.UserEmail).
			Distinct("node_id").
			Pluck("node_id", &nodeIDs).Error; err != nil {
			return banned, err
		}
		banned = append(banned, BannedUser{
			UserEmail:  summary.UserEmail,
			TotalBytes: summary.TotalBytes,
			QuotaBytes: summary.QuotaBytes,
			NodeIDs:    nodeIDs,
		})
	}
	return banned, nil
}

func counterDelta(previous, current uint64) uint64 {
	if current >= previous {
		return current - previous
	}
	// Xray was restarted without a session change; count since the reset.
	return current
}

func saturatingAdd(a, b uint64) uint64 {
	sum := a + b
	if sum < a {
		return ^uint64(0)
	}
	return sum
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func (s *Store) CreateProxyInbound(inbound *ProxyInbound) error {
	return s.db.Create(inbound).Error
}

func (s *Store) UpdateProxyInbound(inbound *ProxyInbound) error {
	result := s.db.Model(&ProxyInbound{}).
		Where("id = ? AND node_id = ?", inbound.ID, inbound.NodeID).
		Updates(map[string]any{
			"remark":        inbound.Remark,
			"port":          inbound.Port,
			"protocol":      inbound.Protocol,
			"network":       inbound.Network,
			"security":      inbound.Security,
			"settings_json": inbound.SettingsJSON,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *Store) DeleteProxyInbound(nodeID string, id uint) error {
	result := s.db.Where("id = ? AND node_id = ?", id, nodeID).Delete(&ProxyInbound{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *Store) GetProxyInbound(nodeID string, id uint) (ProxyInbound, error) {
	var inbound ProxyInbound
	err := s.db.Where("id = ? AND node_id = ?", id, nodeID).First(&inbound).Error
	return inbound, err
}

func (s *Store) ListProxyInbounds(nodeID string) ([]ProxyInbound, error) {
	var inbounds []ProxyInbound
	err := s.db.Where("node_id = ?", nodeID).Order("port ASC").Order("id ASC").Find(&inbounds).Error
	return inbounds, err
}

func (s *Store) GetXrayConfig(nodeID string) (XrayConfig, error) {
	var config XrayConfig
	err := s.db.Where("node_id = ?", nodeID).First(&config).Error
	return config, err
}

func (s *Store) SaveXrayConfig(nodeID, config string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var existing XrayConfig
		err := tx.Where("node_id = ?", nodeID).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&XrayConfig{NodeID: nodeID, Config: config}).Error
		}
		if err != nil {
			return err
		}
		return tx.Model(&existing).Update("config", config).Error
	})
}

func (s *Store) DeleteXrayConfig(nodeID string) error {
	result := s.db.Where("node_id = ?", nodeID).Delete(&XrayConfig{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
