package controlplane

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/store"
)

var certificateDomainPattern = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

type certificateRequest struct {
	Domain  string `json:"domain"`
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}

func (s *Server) handleListCertificates(c *gin.Context) {
	certificates, err := s.store.ListCertificates()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list certificates"})
		return
	}
	views := make([]gin.H, 0, len(certificates))
	for _, certificate := range certificates {
		views = append(views, gin.H{
			"id": certificate.ID, "domain": certificate.Domain,
			"created_at": certificate.CreatedAt, "updated_at": certificate.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"certificates": views})
}

func (s *Server) handleCreateCertificate(c *gin.Context) {
	var request certificateRequest
	if err := bindJSONLimited(c, &request, 2<<20); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	domain := strings.ToLower(strings.TrimSpace(request.Domain))
	certPEM := strings.TrimSpace(request.CertPEM)
	keyPEM := strings.TrimSpace(request.KeyPEM)
	if !certificateDomainPattern.MatchString(domain) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid domain"})
		return
	}
	if certPEM == "" || keyPEM == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cert_pem and key_pem are required"})
		return
	}
	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "certificate/key mismatch: " + err.Error()})
		return
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid certificate: " + err.Error()})
		return
	}
	if !certificateCoversDomain(leaf, domain) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "certificate does not cover " + domain})
		return
	}
	encryptedCert, err := encryptSecret(s.cfg.JWTSecret, certPEM)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt certificate"})
		return
	}
	encryptedKey, err := encryptSecret(s.cfg.JWTSecret, keyPEM)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt private key"})
		return
	}
	record := store.Certificate{Domain: domain, CertPEM: encryptedCert, KeyPEM: encryptedKey}
	if err := s.store.SaveCertificate(&record); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save certificate"})
		return
	}
	s.audit(c, "certificate_create", "", map[string]any{"domain": domain, "id": record.ID})
	c.JSON(http.StatusCreated, gin.H{"id": record.ID, "domain": record.Domain, "created_at": record.CreatedAt})
}

func (s *Server) handleDeleteCertificate(c *gin.Context) {
	id, err := parseUintParam(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid certificate id"})
		return
	}
	if err := s.store.DeleteCertificate(uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete certificate"})
		return
	}
	s.audit(c, "certificate_delete", "", map[string]any{"id": id})
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func certificateCoversDomain(leaf *x509.Certificate, domain string) bool {
	for _, name := range leaf.DNSNames {
		name = strings.ToLower(name)
		if name == domain {
			return true
		}
		if strings.HasPrefix(name, "*.") && strings.HasSuffix(domain, name[1:]) {
			prefix := strings.TrimSuffix(domain, name[1:])
			if prefix != "" && !strings.Contains(prefix, ".") {
				return true
			}
		}
	}
	return false
}

type siteTemplate struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	HTML        string `json:"html"`
}

func siteTemplates() []siteTemplate {
	return []siteTemplate{
		{ID: "corporate", Name: "Corporate Landing", Description: "简洁企业官网 / 产品与服务", HTML: corporateTemplate},
		{ID: "techblog", Name: "Tech Blog", Description: "科技博客 / 文章列表", HTML: techBlogTemplate},
		{ID: "cloud", Name: "Cloud Status", Description: "云服务状态 / 监控首页", HTML: cloudTemplate},
	}
}

func (s *Server) handleListSiteTemplates(c *gin.Context) {
	templates := siteTemplates()
	views := make([]gin.H, 0, len(templates))
	for _, template := range templates {
		views = append(views, gin.H{"id": template.ID, "name": template.Name, "description": template.Description})
	}
	c.JSON(http.StatusOK, gin.H{"templates": views})
}

type siteDeployRequest struct {
	CertificateID uint   `json:"certificate_id"`
	TemplateID    string `json:"template_id"`
}

func (s *Server) handleDeploySite(c *gin.Context) {
	nodeID := c.Param("id")
	if _, err := s.store.GetNode(nodeID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	var request siteDeployRequest
	if err := bindJSONLimited(c, &request, 16<<10); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	certificate, err := s.store.GetCertificate(request.CertificateID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "certificate not found"})
		return
	}
	var selected *siteTemplate
	for _, template := range siteTemplates() {
		if template.ID == request.TemplateID {
			copy := template
			selected = &copy
			break
		}
	}
	if selected == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown site template"})
		return
	}
	certPEM, err := decryptSecret(s.cfg.JWTSecret, certificate.CertPEM)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decrypt certificate"})
		return
	}
	keyPEM, err := decryptSecret(s.cfg.JWTSecret, certificate.KeyPEM)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decrypt certificate key"})
		return
	}
	certPayload, _ := json.Marshal(map[string]any{
		"domain": certificate.Domain, "cert_pem": certPEM, "key_pem": keyPEM,
	})
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()
	certResult, _, err := s.hub.Execute(ctx, nodeID, "cert", certPayload, 60*time.Second)
	if err != nil || certResult.Error != "" {
		message := "failed to install certificate"
		if err != nil {
			message += ": " + err.Error()
		} else {
			message += ": " + certResult.Error
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": message})
		return
	}
	html := strings.ReplaceAll(selected.HTML, "{{DOMAIN}}", certificate.Domain)
	webPayload, _ := json.Marshal(map[string]any{
		"domain": certificate.Domain, "template": selected.ID, "html": html,
	})
	webResult, _, err := s.hub.Execute(ctx, nodeID, "web", webPayload, 60*time.Second)
	if err != nil || webResult.Error != "" {
		message := "certificate installed, but fake site failed"
		if err != nil {
			message += ": " + err.Error()
		} else {
			message += ": " + webResult.Error
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": message})
		return
	}
	s.audit(c, "site_deploy", nodeID, map[string]any{"domain": certificate.Domain, "template": selected.ID})
	c.JSON(http.StatusOK, gin.H{
		"deployed": true, "node_id": nodeID, "domain": certificate.Domain,
		"template": selected.ID, "cert_dir": "/etc/nodehub/certs/" + certificate.Domain,
		"web_root": "/var/www/nodehub-site/" + certificate.Domain,
	})
}

const corporateTemplate = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{DOMAIN}} — Cloud Infrastructure</title><style>body{margin:0;font-family:system-ui,sans-serif;background:#f8fafc;color:#0f172a}header{padding:24px 8%;display:flex;justify-content:space-between;align-items:center;border-bottom:1px solid #e2e8f0}.brand{font-weight:700;letter-spacing:.08em}.hero{padding:96px 8%;max-width:900px}h1{font-size:54px;line-height:1.05;margin:0 0 24px}p{color:#475569;font-size:18px;line-height:1.7}.grid{display:grid;grid-template-columns:repeat(3,1fr);gap:20px;padding:0 8% 96px}.card{background:white;border:1px solid #e2e8f0;border-radius:18px;padding:28px}h3{margin-top:0}footer{padding:28px 8%;color:#64748b;border-top:1px solid #e2e8f0}</style></head><body><header><span class="brand">NORTHSTAR CLOUD</span><span>Solutions · Network · Contact</span></header><section class="hero"><h1>Infrastructure for teams that move fast.</h1><p>Northstar Cloud provides secure connectivity, edge compute and managed network services for modern distributed teams.</p></section><section class="grid"><div class="card"><h3>Edge Network</h3><p>Anycast connectivity with 99.99% availability.</p></div><div class="card"><h3>Managed Compute</h3><p>Elastic instances in 18 regions.</p></div><div class="card"><h3>Enterprise Support</h3><p>24/7 engineering response.</p></div></section><footer>© {{DOMAIN}} · Northstar Cloud</footer></body></html>`

const techBlogTemplate = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{DOMAIN}} — Engineering Notes</title><style>body{margin:0;background:#0b1020;color:#dbe4ff;font-family:ui-monospace,SFMono-Regular,Menlo,monospace}main{max-width:860px;margin:auto;padding:64px 24px}a{color:#7dd3fc;text-decoration:none}h1{font-size:42px;color:#fff;line-height:1.2}.meta{color:#64748b;font-size:13px}.post{border-bottom:1px solid #1e293b;padding:32px 0}.tag{color:#34d399;font-size:12px;letter-spacing:.1em;text-transform:uppercase}p{color:#94a3b8;line-height:1.8}</style></head><body><main><p class="tag">Engineering Notes</p><h1>Building reliable systems at the edge.</h1><p>Notes on networking, observability and distributed infrastructure from the {{DOMAIN}} team.</p><article class="post"><p class="meta">2026-10-18 · 8 min read</p><h2><a href="#">Designing a zero-downtime edge rollout</a></h2><p>How we stage configuration changes across regions without breaking long-lived connections.</p></article><article class="post"><p class="meta">2026-10-11 · 6 min read</p><h2><a href="#">Observability for small infrastructure teams</a></h2><p>A pragmatic telemetry stack that stays useful as the fleet grows.</p></article><article class="post"><p class="meta">2026-10-02 · 11 min read</p><h2><a href="#">TLS automation without operational surprises</a></h2><p>Certificate distribution, rotation and failure modes in a multi-node environment.</p></article></main></body></html>`

const cloudTemplate = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{DOMAIN}} — Service Status</title><style>body{margin:0;background:#f3f7fb;color:#102a43;font-family:system-ui,sans-serif}main{max-width:1000px;margin:auto;padding:64px 24px}h1{font-size:44px;margin-bottom:8px}.status{display:inline-flex;align-items:center;gap:10px;background:#e6fcf5;color:#087f5b;border-radius:999px;padding:10px 18px;font-weight:600}.dot{width:10px;height:10px;border-radius:50%;background:#12b886}.grid{display:grid;grid-template-columns:repeat(4,1fr);gap:16px;margin-top:40px}.metric{background:#fff;border:1px solid #d9e2ec;border-radius:16px;padding:24px}.metric b{display:block;font-size:30px;margin-bottom:6px}table{width:100%;border-collapse:collapse;margin-top:36px;background:#fff;border-radius:16px;overflow:hidden}td,th{padding:16px;text-align:left;border-bottom:1px solid #edf2f7;font-size:14px}th{color:#627d98;font-weight:600}</style></head><body><main><p style="color:#627d98">Cloud Operations</p><h1>All systems operational</h1><div class="status"><span class="dot"></span> 99.99% uptime · last 30 days</div><section class="grid"><div class="metric"><b>18</b>Regions</div><div class="metric"><b>42ms</b>Median latency</div><div class="metric"><b>0</b>Active incidents</div><div class="metric"><b>24/7</b>Monitoring</div></section><table><tr><th>Service</th><th>Region</th><th>Status</th></tr><tr><td>Edge Gateway</td><td>Global</td><td style="color:#087f5b">Operational</td></tr><tr><td>Managed Compute</td><td>APAC</td><td style="color:#087f5b">Operational</td></tr><tr><td>DNS & Certificate</td><td>Global</td><td style="color:#087f5b">Operational</td></tr></table><p style="margin-top:28px;color:#829ab1">© {{DOMAIN}} Cloud Operations</p></main></body></html>`
