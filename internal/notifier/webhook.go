package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type WebhookNotifier struct {
	URL        string
	HTTPClient *http.Client
}

func NewWebhookNotifier(rawURL string) (*WebhookNotifier, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("webhook URL must be http(s)")
	}
	return &WebhookNotifier{
		URL:        parsed.String(),
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (w *WebhookNotifier) Name() string { return "webhook" }

func (w *WebhookNotifier) Send(ctx context.Context, event Event) error {
	payload := map[string]any{
		"content":   event.Text(),
		"text":      event.Text(),
		"event":     event.Type,
		"title":     event.Title,
		"severity":  event.Severity,
		"timestamp": event.Timestamp,
		"fields":    event.Fields,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := w.HTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("webhook HTTP %d", response.StatusCode)
	}
	return nil
}
