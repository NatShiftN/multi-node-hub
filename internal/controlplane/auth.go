package controlplane

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"nodehub/internal/store"
)

const (
	jwtIssuer       = "nodehub"
	maxLoginBody    = 16 << 10
	loginMaxFails   = 10
	loginWindow     = time.Minute
	loginDummyValue = "nodehub-dummy-password"
)

type authManager struct {
	store     *store.Store
	secret    []byte
	ttl       time.Duration
	dummyHash []byte
	logger    *slog.Logger

	loginMu       sync.Mutex
	loginAttempts map[string]loginAttempt
}

type loginAttempt struct {
	count   int
	resetAt time.Time
}

type adminClaims struct {
	UserID      uint     `json:"uid"`
	Username    string   `json:"username"`
	Role        string   `json:"role,omitempty"`
	AllowedTags []string `json:"allowed_tags,omitempty"`
	jwt.RegisteredClaims
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	TOTPCode string `json:"totp_code"`
}

func newAuthManager(persistent *store.Store, cfg Config, logger *slog.Logger) (*authManager, error) {
	secret := []byte(cfg.JWTSecret)
	if len(secret) == 0 {
		generated := make([]byte, 32)
		if _, err := rand.Read(generated); err != nil {
			return nil, err
		}
		secret = generated
		logger.Warn("JWT_SECRET is not set; generated an ephemeral signing key (sessions end on restart)")
	}
	if len(secret) < 32 {
		return nil, errors.New("JWT secret must be at least 32 bytes")
	}
	dummyHash, err := bcrypt.GenerateFromPassword([]byte(loginDummyValue), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	manager := &authManager{
		store:         persistent,
		secret:        secret,
		ttl:           cfg.JWTExpiration,
		dummyHash:     dummyHash,
		logger:        logger,
		loginAttempts: make(map[string]loginAttempt),
	}
	if err := manager.bootstrapAdmin(cfg.AdminUsername, cfg.AdminPassword); err != nil {
		return nil, err
	}
	return manager, nil
}

func (a *authManager) bootstrapAdmin(username, password string) error {
	count, err := a.store.CountAdminUsers()
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	username = strings.TrimSpace(username)
	if username == "" || len(password) < 8 {
		return errors.New("bootstrap admin credentials are invalid")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if _, err := a.store.CreateAdminUser(username, string(hash)); err != nil {
		return err
	}
	a.logger.Warn("bootstrap admin account created; change the default password before public deployment",
		"username", username,
	)
	return nil
}

func (a *authManager) handleLogin(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	remote := c.ClientIP()
	if !a.loginAllowed(remote) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many login attempts; retry later"})
		return
	}

	var request loginRequest
	if err := bindJSONLimited(c, &request, maxLoginBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	username := strings.TrimSpace(request.Username)
	if username == "" || request.Password == "" || len(request.Password) > 1024 {
		a.recordLoginFailure(remote)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
		return
	}

	user, err := a.store.GetAdminUserByUsername(username)
	if err != nil {
		if store.IsNotFound(err) {
			_ = bcrypt.CompareHashAndPassword(a.dummyHash, []byte(request.Password))
		}
		a.recordLoginFailure(remote)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(request.Password)) != nil {
		a.recordLoginFailure(remote)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
		return
	}
	if user.TOTPEnabled {
		if strings.TrimSpace(request.TOTPCode) == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "two-factor code required", "two_factor_required": true,
			})
			return
		}
		if !validateTOTP(user.TOTPSecret, request.TOTPCode, time.Now().UTC()) {
			a.recordLoginFailure(remote)
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "invalid two-factor code", "two_factor_required": true,
			})
			return
		}
	}
	a.clearLoginFailures(remote)

	token, expiresAt, err := a.issueToken(user)
	if err != nil {
		a.logger.Error("issue JWT", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to issue token"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token":      token,
		"expires_at": expiresAt,
		"user": gin.H{
			"id":           user.ID,
			"username":     user.Username,
			"role":         normalizeRole(user.Role),
			"allowed_tags": decodeAllowedTags(user.AllowedTags),
			"totp_enabled": user.TOTPEnabled,
		},
	})
}

func (a *authManager) handleChangePassword(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var request struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := bindJSONLimited(c, &request, maxLoginBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(request.NewPassword) < 8 || len(request.NewPassword) > 1024 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "new password must be 8-1024 characters"})
		return
	}
	userID := c.GetUint("admin_user_id")
	user, err := a.store.GetAdminUserByID(userID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(request.CurrentPassword)) != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "current password is incorrect"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(request.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
		return
	}
	if err := a.store.UpdateAdminPassword(user.ID, string(hash)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update password"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (a *authManager) issueToken(user store.AdminUser) (string, time.Time, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(a.ttl)
	claims := adminClaims{
		UserID:      user.ID,
		Username:    user.Username,
		Role:        normalizeRole(user.Role),
		AllowedTags: decodeAllowedTags(user.AllowedTags),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    jwtIssuer,
			Subject:   user.Username,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-5 * time.Second)),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(a.secret)
	return signed, expiresAt, err
}

func (a *authManager) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := bearerToken(c.GetHeader("Authorization"))
		if raw == "" {
			unauthorized(c)
			return
		}
		claims := &adminClaims{}
		token, err := jwt.ParseWithClaims(
			raw,
			claims,
			func(token *jwt.Token) (any, error) {
				return a.secret, nil
			},
			jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
			jwt.WithIssuer(jwtIssuer),
			jwt.WithExpirationRequired(),
		)
		if err != nil || !token.Valid || claims.Username == "" {
			unauthorized(c)
			return
		}
		user, err := a.store.GetAdminUserByID(claims.UserID)
		if err != nil || user.Username != claims.Username {
			unauthorized(c)
			return
		}
		role := normalizeRole(user.Role)
		tags := decodeAllowedTags(user.AllowedTags)
		c.Set("admin_user_id", user.ID)
		c.Set("admin_username", user.Username)
		c.Set("admin_role", role)
		c.Set("admin_allowed_tags", tags)

		method := c.Request.Method
		if role == store.RoleViewer && method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
			if c.Request.URL.Path != "/api/admin/password" {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "viewer role is read-only"})
				return
			}
		}
		if role == store.RoleOperator && method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
			if nodeID := c.Param("id"); nodeID != "" {
				nodeTags, tagErr := a.store.ListNodeTags(nodeID)
				if tagErr != nil || !tagsIntersect(tags, nodeTags) {
					c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "operator is not allowed to manage this node"})
					return
				}
			}
		}
		c.Next()
	}
}

func (a *authManager) RequireSuperAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("admin_role") != store.RoleSuperAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "superadmin role required"})
			return
		}
		c.Next()
	}
}

func normalizeRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", store.RoleSuperAdmin:
		return store.RoleSuperAdmin
	case store.RoleOperator:
		return store.RoleOperator
	case store.RoleViewer:
		return store.RoleViewer
	default:
		return store.RoleViewer
	}
}

func decodeAllowedTags(raw string) []string {
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return []string{}
	}
	seen := make(map[string]bool, len(tags))
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		result = append(result, tag)
	}
	return result
}

func encodeAllowedTags(tags []string) string {
	normalized := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		normalized = append(normalized, tag)
	}
	raw, _ := json.Marshal(normalized)
	return string(raw)
}

func isHighRiskPlugin(plugin string) bool {
	switch strings.ToLower(strings.TrimSpace(plugin)) {
	case "dd", "reboot", "pppoe_redial":
		return true
	default:
		return false
	}
}

func tagsSubset(requested, allowed []string) bool {
	set := make(map[string]bool, len(allowed))
	for _, tag := range allowed {
		set[tag] = true
	}
	for _, tag := range requested {
		if !set[tag] {
			return false
		}
	}
	return true
}

func tagsIntersect(allowed, nodeTags []string) bool {
	set := make(map[string]bool, len(allowed))
	for _, tag := range allowed {
		set[tag] = true
	}
	for _, tag := range nodeTags {
		if set[tag] {
			return true
		}
	}
	return false
}

func (a *authManager) loginAllowed(remote string) bool {
	now := time.Now()
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	attempt, ok := a.loginAttempts[remote]
	if !ok || now.After(attempt.resetAt) {
		return true
	}
	return attempt.count < loginMaxFails
}

func (a *authManager) recordLoginFailure(remote string) {
	now := time.Now()
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	attempt := a.loginAttempts[remote]
	if now.After(attempt.resetAt) {
		attempt = loginAttempt{resetAt: now.Add(loginWindow)}
	}
	attempt.count++
	a.loginAttempts[remote] = attempt
	if len(a.loginAttempts) > 4096 {
		for key, value := range a.loginAttempts {
			if now.After(value.resetAt) {
				delete(a.loginAttempts, key)
			}
		}
	}
}

func (a *authManager) clearLoginFailures(remote string) {
	a.loginMu.Lock()
	delete(a.loginAttempts, remote)
	a.loginMu.Unlock()
}

func bearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return parts[1]
	}
	return ""
}

func unauthorized(c *gin.Context) {
	c.Header("WWW-Authenticate", "Bearer")
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
}
