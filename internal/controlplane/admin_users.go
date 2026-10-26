package controlplane

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"nodehub/internal/store"
)

var adminUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$`)

type adminUserRequest struct {
	Username    string   `json:"username"`
	Password    string   `json:"password,omitempty"`
	Role        string   `json:"role"`
	AllowedTags []string `json:"allowed_tags"`
}

type adminUserView struct {
	ID          uint     `json:"id"`
	Username    string   `json:"username"`
	Role        string   `json:"role"`
	AllowedTags []string `json:"allowed_tags"`
	CreatedAt   string   `json:"created_at"`
}

func (s *Server) handleListAdminUsers(c *gin.Context) {
	users, err := s.store.ListAdminUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list admin users"})
		return
	}
	views := make([]adminUserView, 0, len(users))
	for _, user := range users {
		views = append(views, adminUserView{
			ID: user.ID, Username: user.Username, Role: normalizeRole(user.Role),
			AllowedTags: decodeAllowedTags(user.AllowedTags), CreatedAt: user.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	c.JSON(http.StatusOK, gin.H{"users": views})
}

func (s *Server) handleCreateAdminUser(c *gin.Context) {
	var request adminUserRequest
	if err := bindJSONLimited(c, &request, 16<<10); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	username := strings.TrimSpace(request.Username)
	if !adminUsernamePattern.MatchString(username) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username must be 3-64 safe characters"})
		return
	}
	if len(request.Password) < 8 || len(request.Password) > 256 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password must be 8-256 characters"})
		return
	}
	role := normalizeRole(request.Role)
	hash, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
		return
	}
	user, err := s.store.CreateAdminUserWithRole(username, string(hash), role, encodeAllowedTags(request.AllowedTags))
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "username already exists"})
		return
	}
	s.audit(c, "admin_user_create", "", map[string]any{
		"user_id": user.ID, "username": user.Username, "role": role, "allowed_tags": request.AllowedTags,
	})
	c.JSON(http.StatusCreated, gin.H{"user": adminUserView{
		ID: user.ID, Username: user.Username, Role: role,
		AllowedTags: decodeAllowedTags(user.AllowedTags), CreatedAt: user.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}})
}

func (s *Server) handleUpdateAdminUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}
	var request adminUserRequest
	if err := bindJSONLimited(c, &request, 16<<10); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	role := normalizeRole(request.Role)
	if err := s.store.UpdateAdminUserAccess(uint(id), role, encodeAllowedTags(request.AllowedTags)); err != nil {
		if store.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "admin user not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update admin user"})
		return
	}
	s.audit(c, "admin_user_update", "", map[string]any{"user_id": id, "role": role, "allowed_tags": request.AllowedTags})
	c.JSON(http.StatusOK, gin.H{"updated": true, "id": id})
}

func (s *Server) handleDeleteAdminUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}
	if uint(id) == c.GetUint("admin_user_id") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot delete the current account"})
		return
	}
	if err := s.store.DeleteAdminUser(uint(id)); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "admin user not found"})
		return
	}
	s.audit(c, "admin_user_delete", "", map[string]any{"user_id": id})
	c.JSON(http.StatusOK, gin.H{"deleted": true, "id": id})
}
