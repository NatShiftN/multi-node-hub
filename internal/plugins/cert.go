package plugins

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
)

var siteDomainPattern = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

type CertPlugin struct {
	logger *slog.Logger
}

type certPayload struct {
	Domain  string `json:"domain"`
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}

func NewCertPlugin(logger *slog.Logger) *CertPlugin {
	if logger == nil {
		logger = slog.Default()
	}
	return &CertPlugin{logger: logger}
}

func (p *CertPlugin) Name() string { return "cert" }

func (p *CertPlugin) Execute(_ context.Context, raw json.RawMessage) (any, error) {
	var request certPayload
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, fmt.Errorf("decode certificate payload: %w", err)
	}
	domain := request.Domain
	if !siteDomainPattern.MatchString(domain) || len(request.CertPEM) > 1<<20 || len(request.KeyPEM) > 1<<20 {
		return nil, errors.New("invalid certificate payload")
	}
	if _, err := tls.X509KeyPair([]byte(request.CertPEM), []byte(request.KeyPEM)); err != nil {
		return nil, fmt.Errorf("certificate/key mismatch: %w", err)
	}
	dir := filepath.Join("/etc/nodehub/certs", domain)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := writePrivateFile(certPath, []byte(request.CertPEM), 0o644); err != nil {
		return nil, err
	}
	if err := writePrivateFile(keyPath, []byte(request.KeyPEM), 0o600); err != nil {
		return nil, err
	}
	p.logger.Info("certificate installed", "domain", domain, "dir", dir)
	return map[string]any{"domain": domain, "cert_path": certPath, "key_path": keyPath, "key_mode": "0600"}, nil
}

func writePrivateFile(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}
