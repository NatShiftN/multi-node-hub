package controlplane

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func (s *Server) handleTOTPStatus(c *gin.Context) {
	user, err := s.store.GetAdminUserByID(c.GetUint("admin_user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": user.TOTPEnabled})
}

func (s *Server) handleTOTPSetup(c *gin.Context) {
	user, err := s.store.GetAdminUserByID(c.GetUint("admin_user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	if user.TOTPEnabled {
		c.JSON(http.StatusConflict, gin.H{"error": "2FA is already enabled"})
		return
	}
	secret, err := generateTOTPSecret()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate secret"})
		return
	}
	if err := s.store.SetAdminTOTP(user.ID, secret, false); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save secret"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"secret":         secret,
		"otpauth_url":    totpProvisioningURL("NodeHub", user.Username, secret),
		"enabled":        false,
		"period_seconds": 30,
	})
}

func (s *Server) handleTOTPEnable(c *gin.Context) {
	var request struct {
		Code string `json:"code"`
	}
	if err := bindJSONLimited(c, &request, 8<<10); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	user, err := s.store.GetAdminUserByID(c.GetUint("admin_user_id"))
	if err != nil || user.TOTPSecret == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "run setup first"})
		return
	}
	if !validateTOTP(user.TOTPSecret, request.Code, time.Now().UTC()) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 2FA code"})
		return
	}
	if err := s.store.SetAdminTOTP(user.ID, user.TOTPSecret, true); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enable 2FA"})
		return
	}
	s.audit(c, "totp_enable", "", nil)
	c.JSON(http.StatusOK, gin.H{"enabled": true})
}

func (s *Server) handleTOTPDisable(c *gin.Context) {
	var request struct {
		Password string `json:"password"`
	}
	if err := bindJSONLimited(c, &request, 8<<10); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	user, err := s.store.GetAdminUserByID(c.GetUint("admin_user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(strings.TrimSpace(request.Password))) != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "password is incorrect"})
		return
	}
	if err := s.store.SetAdminTOTP(user.ID, "", false); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to disable 2FA"})
		return
	}
	s.audit(c, "totp_disable", "", nil)
	c.JSON(http.StatusOK, gin.H{"enabled": false})
}
