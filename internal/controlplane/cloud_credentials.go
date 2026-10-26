package controlplane

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/cloud"
)

type cloudCredentialRequest struct {
	Provider string `json:"provider"`
	APIKey   string `json:"api_key"`
}

func (s *Server) handleListCloudCredentials(c *gin.Context) {
	credentials, err := s.store.ListCloudCredentials()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list cloud credentials"})
		return
	}
	views := make([]gin.H, 0, len(credentials))
	for _, credential := range credentials {
		plain, err := decryptSecret(s.cfg.JWTSecret, credential.APIKey)
		if err != nil {
			continue
		}
		views = append(views, gin.H{
			"provider":   credential.Provider,
			"masked_key": maskCredential(plain),
			"updated_at": credential.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"credentials": views})
}

func (s *Server) handleSetCloudCredential(c *gin.Context) {
	var request cloudCredentialRequest
	if err := bindJSONLimited(c, &request, 16<<10); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	provider := strings.ToLower(strings.TrimSpace(request.Provider))
	key := strings.TrimSpace(request.APIKey)
	if provider != "vultr" && provider != "digitalocean" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider must be vultr or digitalocean"})
		return
	}
	if key == "" || len(key) > 4096 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "api_key is required"})
		return
	}
	encrypted, err := encryptSecret(s.cfg.JWTSecret, key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt cloud credential"})
		return
	}
	if err := s.store.SetCloudCredential(provider, encrypted, time.Now().UTC()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save cloud credential"})
		return
	}
	s.audit(c, "cloud_credential_set", "", map[string]any{"provider": provider})
	c.JSON(http.StatusOK, gin.H{"provider": provider, "masked_key": maskCredential(key)})
}

func (s *Server) handleTestCloudCredential(c *gin.Context) {
	var request cloudCredentialRequest
	if err := bindJSONLimited(c, &request, 16<<10); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	providerName := strings.ToLower(strings.TrimSpace(request.Provider))
	key := strings.TrimSpace(request.APIKey)
	if key == "" {
		if credential, err := s.store.GetCloudCredential(providerName); err == nil {
			key, _ = decryptSecret(s.cfg.JWTSecret, credential.APIKey)
		}
	}
	var provider cloud.Provider
	switch providerName {
	case "vultr":
		provider = cloud.NewVultrProvider(key, s.cfg.VultrBaseURL, s.logger)
	case "digitalocean":
		provider = cloud.NewDigitalOceanProvider(key, s.cfg.DigitalOceanBaseURL, s.logger)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider must be vultr or digitalocean"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	if err := provider.Validate(ctx); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "provider": providerName})
}

func (s *Server) handleDeleteCloudCredential(c *gin.Context) {
	provider := strings.ToLower(strings.TrimSpace(c.Param("provider")))
	if err := s.store.DeleteCloudCredential(provider); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete cloud credential"})
		return
	}
	s.audit(c, "cloud_credential_delete", "", map[string]any{"provider": provider})
	c.JSON(http.StatusOK, gin.H{"deleted": true, "provider": provider})
}

func maskCredential(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 8 {
		return "••••"
	}
	return value[:4] + "…" + value[len(value)-4:]
}
