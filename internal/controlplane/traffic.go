package controlplane

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/store"
)

type userTrafficResponse struct {
	NodeID        string    `json:"node_id"`
	UserEmail     string    `json:"user_email"`
	UploadBytes   uint64    `json:"upload_bytes"`
	DownloadBytes uint64    `json:"download_bytes"`
	TotalBytes    uint64    `json:"total_bytes"`
	QuotaBytes    uint64    `json:"quota_bytes"`
	IsBanned      bool      `json:"is_banned"`
	LastUpdated   time.Time `json:"last_updated"`
}

func (s *Server) handleListUserTraffic(c *gin.Context) {
	nodeID := c.Param("id")
	if _, err := s.store.GetNode(nodeID); err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read node"})
		return
	}
	rows, err := s.store.ListUserTraffic(nodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list user traffic"})
		return
	}
	quotas, err := s.store.ListTrafficQuotas()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list traffic quotas"})
		return
	}
	quotaByEmail := make(map[string]store.TrafficUser, len(quotas))
	for _, quota := range quotas {
		quotaByEmail[quota.UserEmail] = quota
	}
	result := make([]userTrafficResponse, 0, len(rows))
	for _, row := range rows {
		total := row.UploadBytes + row.DownloadBytes
		if total < row.UploadBytes {
			total = ^uint64(0)
		}
		quota := quotaByEmail[row.UserEmail]
		result = append(result, userTrafficResponse{
			NodeID:        row.NodeID,
			UserEmail:     row.UserEmail,
			UploadBytes:   row.UploadBytes,
			DownloadBytes: row.DownloadBytes,
			TotalBytes:    total,
			QuotaBytes:    quota.QuotaBytes,
			IsBanned:      quota.IsBanned,
			LastUpdated:   row.LastUpdated,
		})
	}
	c.JSON(http.StatusOK, gin.H{"users": result})
}

func (s *Server) handleListTrafficUsers(c *gin.Context) {
	users, err := s.store.ListTrafficUserSummaries()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list traffic users"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}

func (s *Server) handleTrafficHistory(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "30"))
	rows, err := s.store.ListTrafficDaily(days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list traffic history"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"days": days, "history": rows})
}

func (s *Server) handleBanUser(c *gin.Context) {
	email := strings.TrimSpace(c.Param("email"))
	nodeIDs, err := s.store.BanTrafficUser(email, time.Now().UTC())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	for _, nodeID := range nodeIDs {
		s.enforceBannedUser(c.Request.Context(), nodeID, email)
	}
	s.audit(c, "traffic_ban", "", map[string]any{"user_email": email, "node_ids": nodeIDs})
	c.JSON(http.StatusOK, gin.H{"user_email": email, "is_banned": true, "node_ids": nodeIDs})
}

func (s *Server) handleUnbanUser(c *gin.Context) {
	email := strings.TrimSpace(c.Param("email"))
	if err := s.store.UnbanTrafficUser(email); err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unban user"})
		return
	}
	s.audit(c, "traffic_unban", "", map[string]any{"user_email": email})
	c.JSON(http.StatusOK, gin.H{"user_email": email, "is_banned": false})
}

func (s *Server) handleSetUserQuota(c *gin.Context) {
	email := strings.TrimSpace(c.Param("email"))
	if email == "" || len(email) > 255 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user email"})
		return
	}
	var request struct {
		QuotaBytes uint64 `json:"quota_bytes"`
	}
	if err := bindJSONLimited(c, &request, 16<<10); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := s.store.SetUserQuota(email, request.QuotaBytes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to set quota"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"user_email":  email,
		"quota_bytes": request.QuotaBytes,
	})
}
