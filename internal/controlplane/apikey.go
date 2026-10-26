package controlplane

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/store"
)

const (
	apiKeyPrefix  = "sk-"
	maxAPIKeyBody = 4 << 10
)

type apiKeyCreateRequest struct {
	Name string `json:"name"`
}

type apiKeyView struct {
	ID         uint       `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Revoked    bool       `json:"revoked"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

func newAPIKeyToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token := apiKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return token, hashAPIKey(token), nil
}

func hashAPIKey(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func (s *Server) apiKeyMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := strings.TrimSpace(c.GetHeader("Authorization"))
		const bearer = "Bearer "
		if !strings.HasPrefix(header, bearer) {
			c.Header("WWW-Authenticate", `Bearer realm="nodehub"`)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing API key"})
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(header, bearer))
		if !strings.HasPrefix(token, apiKeyPrefix) {
			c.Header("WWW-Authenticate", `Bearer realm="nodehub"`)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
			return
		}
		key, err := s.store.GetAPIKeyByHash(hashAPIKey(token))
		if err != nil {
			c.Header("WWW-Authenticate", `Bearer realm="nodehub"`)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
			return
		}
		c.Set("api_key_name", key.Name)
		c.Set("api_key_prefix", key.Prefix)
		_ = s.store.TouchAPIKey(key.ID, time.Now().UTC())
		c.Next()
	}
}

func (s *Server) handleListAPIKeys(c *gin.Context) {
	keys, err := s.store.ListAPIKeys()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list API keys"})
		return
	}
	views := make([]apiKeyView, 0, len(keys))
	for _, key := range keys {
		views = append(views, apiKeyViewFromStore(key))
	}
	c.JSON(http.StatusOK, gin.H{"api_keys": views})
}

func (s *Server) handleCreateAPIKey(c *gin.Context) {
	var request apiKeyCreateRequest
	if err := bindJSONLimited(c, &request, maxAPIKeyBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	name := strings.TrimSpace(request.Name)
	if name == "" || len(name) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name must be 1-128 characters"})
		return
	}
	token, hash, err := newAPIKeyToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate API key"})
		return
	}
	prefix := token
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	record := store.APIKey{Name: name, KeyHash: hash, Prefix: prefix}
	if err := s.store.CreateAPIKey(&record); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist API key"})
		return
	}
	s.audit(c, "api_key_create", "", map[string]any{"name": name, "prefix": prefix, "id": record.ID})
	c.JSON(http.StatusCreated, gin.H{
		"api_key": apiKeyViewFromStore(record),
		"token":   token,
		"warning": "store this token now; it will not be shown again",
	})
}

func (s *Server) handleRevokeAPIKey(c *gin.Context) {
	id, err := parseUintParam(c.Param("id"))
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid API key id"})
		return
	}
	if err := s.store.RevokeAPIKey(uint(id)); err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "API key not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke API key"})
		return
	}
	s.audit(c, "api_key_revoke", "", map[string]any{"id": id})
	c.JSON(http.StatusOK, gin.H{"revoked": true, "id": id})
}

func apiKeyViewFromStore(key store.APIKey) apiKeyView {
	return apiKeyView{
		ID:         key.ID,
		Name:       key.Name,
		Prefix:     key.Prefix,
		Revoked:    key.Revoked,
		LastUsedAt: key.LastUsedAt,
		CreatedAt:  key.CreatedAt,
	}
}

func parseUintParam(raw string) (uint64, error) {
	return strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
}
