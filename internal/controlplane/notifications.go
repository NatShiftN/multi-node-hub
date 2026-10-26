package controlplane

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/notifier"
	"nodehub/internal/store"
)

func (s *Server) applyNotificationSettings() {
	setting, err := s.store.GetNotificationSetting()
	if err != nil {
		setting = store.NotificationSetting{}
	}
	token, _ := decryptSecret(s.cfg.JWTSecret, setting.TelegramToken)
	chatID := strings.TrimSpace(setting.TelegramChatID)
	webhook, _ := decryptSecret(s.cfg.JWTSecret, setting.WebhookURL)
	token = strings.TrimSpace(token)
	webhook = strings.TrimSpace(webhook)
	if token == "" && chatID == "" && webhook == "" {
		token = strings.TrimSpace(s.cfg.TelegramBotToken)
		chatID = strings.TrimSpace(s.cfg.TelegramChatID)
		webhook = strings.TrimSpace(s.cfg.WebhookURL)
	}
	targets := make([]notifier.Notifier, 0, 2)
	if token != "" && chatID != "" {
		if target, err := notifier.NewTelegramNotifier(token, chatID); err == nil {
			targets = append(targets, target)
		} else {
			s.logger.Warn("configure Telegram notifier", "error", err)
		}
	}
	if webhook != "" {
		if target, err := notifier.NewWebhookNotifier(webhook); err == nil {
			targets = append(targets, target)
		} else {
			s.logger.Warn("configure webhook notifier", "error", err)
		}
	}
	s.notifications.SetNotifiers(targets)
}

func (s *Server) handleGetNotificationSettings(c *gin.Context) {
	setting, err := s.store.GetNotificationSetting()
	if err != nil {
		setting = store.NotificationSetting{}
	}
	c.JSON(http.StatusOK, gin.H{
		"telegram_configured": setting.TelegramToken != "" && setting.TelegramChatID != "",
		"telegram_chat_id":    setting.TelegramChatID,
		"webhook_configured":  setting.WebhookURL != "",
		"updated_at":          setting.UpdatedAt,
	})
}

func (s *Server) handleSaveNotificationSettings(c *gin.Context) {
	var request struct {
		TelegramToken  string `json:"telegram_bot_token"`
		TelegramChatID string `json:"telegram_chat_id"`
		WebhookURL     string `json:"webhook_url"`
	}
	if err := bindJSONLimited(c, &request, 16<<10); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	encryptedToken, err := encryptSecret(s.cfg.JWTSecret, strings.TrimSpace(request.TelegramToken))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt Telegram token"})
		return
	}
	encryptedWebhook, err := encryptSecret(s.cfg.JWTSecret, strings.TrimSpace(request.WebhookURL))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt webhook URL"})
		return
	}
	setting := store.NotificationSetting{
		TelegramToken:  encryptedToken,
		TelegramChatID: strings.TrimSpace(request.TelegramChatID),
		WebhookURL:     encryptedWebhook,
		UpdatedAt:      time.Now().UTC(),
	}
	if setting.TelegramToken != "" && setting.TelegramChatID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "telegram_chat_id is required with a bot token"})
		return
	}
	if err := s.store.SaveNotificationSetting(setting); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save notification settings"})
		return
	}
	s.applyNotificationSettings()
	s.audit(c, "notification_settings_save", "", map[string]any{
		"telegram": setting.TelegramToken != "" && setting.TelegramChatID != "",
		"webhook":  setting.WebhookURL != "",
	})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleTestNotification(c *gin.Context) {
	s.applyNotificationSettings()
	if !s.notifications.HasNotifiers() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no notification target configured"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	s.notifications.SendNow(ctx, notifier.Event{
		Type: "test_notification", Title: "NodeHub test notification",
		Message:  "If you can read this, Telegram/webhook delivery is working.",
		Severity: "info",
	})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
