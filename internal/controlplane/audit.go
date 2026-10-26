package controlplane

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/store"
)

func (s *Server) audit(c *gin.Context, action, target string, details any) {
	username := "system"
	ipAddress := ""
	if c != nil {
		if value := strings.TrimSpace(c.GetString("admin_username")); value != "" {
			username = value
		}
		ipAddress = c.ClientIP()
	}
	s.writeAudit(username, action, target, details, ipAddress)
}

func (s *Server) auditSystem(username, action, target string, details any) {
	if username == "" {
		username = "system"
	}
	s.writeAudit(username, action, target, details, "")
}

func (s *Server) writeAudit(username, action, target string, details any, ipAddress string) {
	raw, err := json.Marshal(details)
	if err != nil {
		raw = []byte(`{"error":"details encoding failed"}`)
	}
	if len(raw) > 8192 {
		raw = raw[:8192]
	}
	entry := store.AuditLog{
		Timestamp:    time.Now().UTC(),
		Username:     username,
		Action:       action,
		TargetNodeID: target,
		Details:      string(raw),
		IPAddress:    ipAddress,
	}
	if err := s.store.CreateAuditLog(entry); err != nil {
		s.logger.Error("write audit log", "action", action, "target", target, "error", err)
	}
}

func (s *Server) handleListAuditLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	entries, total, err := s.store.ListAuditLogs(page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list audit logs"})
		return
	}
	totalPages := int64(0)
	if pageSize > 0 {
		totalPages = (total + int64(pageSize) - 1) / int64(pageSize)
	}
	c.JSON(http.StatusOK, gin.H{
		"logs":        entries,
		"total":       total,
		"page":        page,
		"page_size":   pageSize,
		"total_pages": totalPages,
	})
}
