package controlplane

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/store"
)

type dashboardStats struct {
	NodesTotal        int64   `json:"nodes_total"`
	NodesOnline       int64   `json:"nodes_online"`
	NodesOffline      int64   `json:"nodes_offline"`
	NodesGFWBlocked   int64   `json:"nodes_gfw_blocked"`
	NodesProvisioning int64   `json:"nodes_provisioning"`
	MonthlyUpload     uint64  `json:"monthly_upload_bytes"`
	MonthlyDownload   uint64  `json:"monthly_download_bytes"`
	BannedUsers       int64   `json:"banned_users"`
	AverageCPU        float64 `json:"average_cpu_percent"`
	AverageMemory     float64 `json:"average_memory_percent"`
	TelemetryNodes    int     `json:"telemetry_nodes"`
}

func (s *Server) handleDashboardStats(c *gin.Context) {
	stats, err := s.dashboardStatsData()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, stats)
}

func (s *Server) dashboardStatsData() (dashboardStats, error) {
	counts, err := s.store.CountNodesByStatus()
	if err != nil {
		return dashboardStats{}, err
	}
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	upload, download, err := s.store.MonthlyTraffic(monthStart)
	if err != nil {
		return dashboardStats{}, err
	}
	banned, err := s.store.CountBannedUsers()
	if err != nil {
		return dashboardStats{}, err
	}
	cpu, memory, telemetryNodes := s.telemetry.AverageLoad()
	var total int64
	for _, count := range counts {
		total += count
	}
	online := counts[store.NodeStatusOnline]
	return dashboardStats{
		NodesTotal:        total,
		NodesOnline:       online,
		NodesOffline:      total - online,
		NodesGFWBlocked:   counts[store.NodeStatusGFWBlocked],
		NodesProvisioning: counts[store.NodeStatusProvisioning],
		MonthlyUpload:     upload,
		MonthlyDownload:   download,
		BannedUsers:       banned,
		AverageCPU:        cpu,
		AverageMemory:     memory,
		TelemetryNodes:    telemetryNodes,
	}, nil
}
