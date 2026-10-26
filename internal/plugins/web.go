package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type WebPlugin struct {
	logger *slog.Logger
}

type webPayload struct {
	Domain   string `json:"domain"`
	Template string `json:"template"`
	HTML     string `json:"html"`
}

func NewWebPlugin(logger *slog.Logger) *WebPlugin {
	if logger == nil {
		logger = slog.Default()
	}
	return &WebPlugin{logger: logger}
}

func (p *WebPlugin) Name() string { return "web" }

func (p *WebPlugin) Execute(ctx context.Context, raw json.RawMessage) (any, error) {
	var request webPayload
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, fmt.Errorf("decode web payload: %w", err)
	}
	if !siteDomainPattern.MatchString(request.Domain) {
		return nil, errors.New("invalid site domain")
	}
	if strings.TrimSpace(request.HTML) == "" || len(request.HTML) > 2<<20 {
		return nil, errors.New("invalid site HTML")
	}
	root := filepath.Join("/var/www/nodehub-site", request.Domain)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	indexPath := filepath.Join(root, "index.html")
	if err := os.WriteFile(indexPath, []byte(request.HTML), 0o644); err != nil {
		return nil, err
	}
	if _, err := exec.LookPath("nginx"); err != nil {
		return nil, errors.New("nginx is not installed; cert/site files were written")
	}
	certDir := filepath.Join("/etc/nodehub/certs", request.Domain)
	config := fmt.Sprintf(`server {
    listen 443 ssl http2;
    server_name %s;
    ssl_certificate %s/cert.pem;
    ssl_certificate_key %s/key.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    root %s;
    index index.html;
    location / { try_files $uri $uri/ /index.html; }
}
`, request.Domain, certDir, certDir, root)
	available := filepath.Join("/etc/nginx/sites-available", "nodehub-site-"+request.Domain)
	enabled := filepath.Join("/etc/nginx/sites-enabled", "nodehub-site-"+request.Domain)
	if err := os.WriteFile(available, []byte(config), 0o644); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(enabled), 0o755); err != nil {
		return nil, err
	}
	_ = os.Remove(enabled)
	if err := os.Symlink(available, enabled); err != nil {
		return nil, err
	}
	testCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(testCtx, "nginx", "-t").CombinedOutput(); err != nil {
		return nil, fmt.Errorf("nginx -t failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	reloadCtx, reloadCancel := context.WithTimeout(ctx, 20*time.Second)
	defer reloadCancel()
	if output, err := exec.CommandContext(reloadCtx, "systemctl", "reload", "nginx").CombinedOutput(); err != nil {
		return nil, fmt.Errorf("reload nginx: %w: %s", err, strings.TrimSpace(string(output)))
	}
	p.logger.Info("fake site deployed", "domain", request.Domain, "template", request.Template, "root", root)
	return map[string]any{"domain": request.Domain, "root": root, "config": available, "reloaded": true}, nil
}
