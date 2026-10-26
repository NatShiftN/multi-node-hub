package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type TelegramNotifier struct {
	Token      string
	ChatID     string
	BaseURL    string
	HTTPClient *http.Client
}

func NewTelegramNotifier(token, chatID string) (*TelegramNotifier, error) {
	token = strings.TrimSpace(token)
	chatID = strings.TrimSpace(chatID)
	if token == "" || chatID == "" {
		return nil, errors.New("Telegram bot token and chat id are required")
	}
	return &TelegramNotifier{
		Token:      token,
		ChatID:     chatID,
		BaseURL:    "https://api.telegram.org",
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (t *TelegramNotifier) Name() string { return "telegram" }

func (t *TelegramNotifier) SendDocument(ctx context.Context, path, caption string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	if err := writer.WriteField("chat_id", t.ChatID); err != nil {
		return err
	}
	if caption != "" {
		if err := writer.WriteField("caption", caption); err != nil {
			return err
		}
	}
	part, err := writer.CreateFormFile("document", filepath.Base(path))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	endpoint := strings.TrimRight(t.BaseURL, "/") + "/bot" + t.Token + "/sendDocument"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buffer)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := t.HTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Telegram HTTP %d", response.StatusCode)
	}
	return nil
}

func (t *TelegramNotifier) Send(ctx context.Context, event Event) error {
	body := map[string]any{
		"chat_id":                  t.ChatID,
		"text":                     event.Text(),
		"disable_web_page_preview": true,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(t.BaseURL, "/") + "/bot" + t.Token + "/sendMessage"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := t.HTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Telegram HTTP %d", response.StatusCode)
	}
	return nil
}
