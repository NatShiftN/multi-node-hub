package controlplane

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/protocol"
)

func buildAgentRelease(cfg Config) (*protocol.AgentVersionInfo, error) {
	file, err := os.Open(cfg.AgentBinaryPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return nil, err
	}
	return &protocol.AgentVersionInfo{
		Version:     cfg.AgentVersion,
		OS:          "linux",
		Arch:        "amd64",
		DownloadURL: cfg.PublicURL + "/downloads/nodehub-agent-linux-amd64",
		SHA256:      hex.EncodeToString(hasher.Sum(nil)),
		Size:        stat.Size(),
	}, nil
}

func (s *Server) handleAgentVersion(c *gin.Context) {
	if s.agentRelease == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent binary is not available"})
		return
	}
	c.JSON(http.StatusOK, s.agentRelease)
}

func (s *Server) sendVersionInfo(node *Node) {
	if s.agentRelease == nil {
		return
	}
	env := protocol.Envelope{
		Type:    protocol.TypeVersionInfo,
		Version: s.agentRelease,
		SentAt:  time.Now().UTC(),
	}
	raw, err := json.Marshal(env)
	if err != nil {
		s.logger.Warn("encode version_info", "error", err)
		return
	}
	if err := node.enqueue(raw); err != nil {
		s.logger.Debug("enqueue version_info", "agent_id", node.id, "error", err)
	}
}
