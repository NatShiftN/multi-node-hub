package ddns

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

type CloudflareProvider struct {
	APIToken   string
	BaseURL    string
	HTTPClient *http.Client
}

type cloudflareResponse struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result json.RawMessage `json:"result"`
}

func NewCloudflareProvider(apiToken, baseURL string) *CloudflareProvider {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.cloudflare.com/client/v4"
	}
	return &CloudflareProvider{
		APIToken:   strings.TrimSpace(apiToken),
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *CloudflareProvider) Name() string { return "cloudflare" }

func (c *CloudflareProvider) Update(ctx context.Context, request Request) error {
	if c.APIToken == "" {
		return errors.New("CLOUDFLARE_API_TOKEN is not configured")
	}
	if request.ZoneID == "" || request.Domain == "" {
		return errors.New("zone id and domain are required")
	}
	recordID := strings.TrimSpace(request.RecordID)
	if recordID == "" {
		var err error
		recordID, err = c.findRecordID(ctx, request)
		if err != nil {
			return err
		}
	}
	body := map[string]any{
		"type":    "A",
		"name":    request.Domain,
		"content": request.IP,
		"ttl":     60,
		"proxied": false,
	}
	var response cloudflareResponse
	path := "/zones/" + url.PathEscape(request.ZoneID) + "/dns_records/" + url.PathEscape(recordID)
	if err := c.request(ctx, http.MethodPut, path, body, &response); err != nil {
		return err
	}
	if !response.Success {
		return cloudflareError(response)
	}
	return nil
}

func (c *CloudflareProvider) findRecordID(ctx context.Context, request Request) (string, error) {
	query := url.Values{}
	query.Set("type", "A")
	query.Set("name", request.Domain)
	path := "/zones/" + url.PathEscape(request.ZoneID) + "/dns_records?" + query.Encode()
	var response cloudflareResponse
	if err := c.request(ctx, http.MethodGet, path, nil, &response); err != nil {
		return "", err
	}
	if !response.Success {
		return "", cloudflareError(response)
	}
	var records []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Result, &records); err != nil {
		return "", err
	}
	if len(records) == 0 || records[0].ID == "" {
		return "", fmt.Errorf("Cloudflare A record not found for %s", request.Domain)
	}
	return records[0].ID, nil
}

func (c *CloudflareProvider) request(ctx context.Context, method, path string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIToken)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Cloudflare API HTTP %d", response.StatusCode)
	}
	if target != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, target); err != nil {
			return err
		}
	}
	return nil
}

func cloudflareError(response cloudflareResponse) error {
	if len(response.Errors) == 0 {
		return errors.New("Cloudflare API returned success=false")
	}
	messages := make([]string, 0, len(response.Errors))
	for _, item := range response.Errors {
		messages = append(messages, fmt.Sprintf("%d: %s", item.Code, item.Message))
	}
	return errors.New("Cloudflare API: " + strings.Join(messages, "; "))
}
