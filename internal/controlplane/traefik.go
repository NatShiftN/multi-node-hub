package controlplane

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/store"
)

type traefikServer struct {
	URL    string `json:"url"`
	Status string `json:"status,omitempty"`
}

type traefikRouteView struct {
	Name        string          `json:"name"`
	Rule        string          `json:"rule"`
	EntryPoints []string        `json:"entry_points"`
	Service     string          `json:"service"`
	Middlewares []string        `json:"middlewares"`
	TLS         bool            `json:"tls"`
	Status      string          `json:"status"`
	Servers     []traefikServer `json:"servers"`
}

type traefikRawRouter struct {
	Name        string   `json:"name"`
	Rule        string   `json:"rule"`
	EntryPoints []string `json:"entryPoints"`
	Service     string   `json:"service"`
	Middlewares []string `json:"middlewares"`
	Status      string   `json:"status"`
	TLS         *struct {
		CertResolver string `json:"certResolver"`
	} `json:"tls"`
}

type traefikRawService struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Status       string `json:"status"`
	LoadBalancer struct {
		Servers []struct {
			URL    string `json:"url"`
			Status string `json:"status"`
		} `json:"servers"`
	} `json:"loadBalancer"`
}

func (s *Server) traefikSetting() (store.TraefikSetting, error) {
	setting, err := s.store.GetTraefikSetting()
	if err != nil {
		return store.TraefikSetting{}, err
	}
	return setting, nil
}

func (s *Server) traefikClient(setting store.TraefikSetting) (*http.Client, string, string, error) {
	raw := strings.TrimRight(strings.TrimSpace(setting.APIURL), "/")
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, "", "", fmt.Errorf("Traefik API URL must be http(s)")
	}
	password, err := decryptSecret(s.cfg.JWTSecret, setting.Password)
	if err != nil {
		return nil, "", "", err
	}
	return &http.Client{Timeout: 15 * time.Second}, raw, password, nil
}

func (s *Server) handleGetTraefikSettings(c *gin.Context) {
	setting, err := s.store.GetTraefikSetting()
	if err != nil {
		setting = store.TraefikSetting{}
	}
	c.JSON(http.StatusOK, gin.H{
		"api_url":             setting.APIURL,
		"username":            setting.Username,
		"log_path":            setting.LogPath,
		"password_configured": setting.Password != "",
		"updated_at":          setting.UpdatedAt,
	})
}

func (s *Server) handleSaveTraefikSettings(c *gin.Context) {
	var request struct {
		APIURL   string `json:"api_url"`
		Username string `json:"username"`
		Password string `json:"password"`
		LogPath  string `json:"log_path"`
	}
	if err := bindJSONLimited(c, &request, 32<<10); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	apiURL := strings.TrimRight(strings.TrimSpace(request.APIURL), "/")
	if apiURL != "" {
		parsed, err := url.Parse(apiURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "api_url must be http(s)"})
			return
		}
	}
	encrypted := ""
	if strings.TrimSpace(request.Password) == "" {
		if existing, err := s.store.GetTraefikSetting(); err == nil {
			encrypted = existing.Password
		}
	} else {
		value, err := encryptSecret(s.cfg.JWTSecret, request.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt Traefik password"})
			return
		}
		encrypted = value
	}
	if err := s.store.SaveTraefikSetting(store.TraefikSetting{
		APIURL: apiURL, Username: strings.TrimSpace(request.Username),
		Password: encrypted, LogPath: strings.TrimSpace(request.LogPath), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save Traefik settings"})
		return
	}
	s.audit(c, "traefik_settings_save", "", map[string]any{"api_url": apiURL, "log_path": request.LogPath})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) traefikGetJSON(ctx context.Context, setting store.TraefikSetting, path string, target any) error {
	client, base, password, err := s.traefikClient(setting)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	if setting.Username != "" || password != "" {
		request.SetBasicAuth(setting.Username, password)
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Traefik HTTP %d", response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(target)
}

func (s *Server) handleTestTraefik(c *gin.Context) {
	setting, err := s.store.GetTraefikSetting()
	if err != nil || setting.APIURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "configure Traefik API first"})
		return
	}
	var overview map[string]any
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	if err := s.traefikGetJSON(ctx, setting, "/api/overview", &overview); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "overview": overview})
}

func (s *Server) handleTraefikRoutes(c *gin.Context) {
	if s.cfg.Demo {
		c.JSON(http.StatusOK, gin.H{"demo": true, "routes": demoTrafikRoutes(), "overview": gin.H{"http": gin.H{"routers": 3, "services": 3}}})
		return
	}
	setting, err := s.store.GetTraefikSetting()
	if err != nil || setting.APIURL == "" {
		c.JSON(http.StatusOK, gin.H{"configured": false, "routes": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	var routers []traefikRawRouter
	var services []traefikRawService
	var overview map[string]any
	if err := s.traefikGetJSON(ctx, setting, "/api/http/routers", &routers); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "read Traefik routers: " + err.Error()})
		return
	}
	if err := s.traefikGetJSON(ctx, setting, "/api/http/services", &services); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "read Traefik services: " + err.Error()})
		return
	}
	_ = s.traefikGetJSON(ctx, setting, "/api/overview", &overview)
	serviceServers := make(map[string][]traefikServer, len(services))
	for _, service := range services {
		for _, server := range service.LoadBalancer.Servers {
			serviceServers[service.Name] = append(serviceServers[service.Name], traefikServer{
				URL: server.URL, Status: server.Status,
			})
		}
	}
	routes := make([]traefikRouteView, 0, len(routers))
	for _, router := range routers {
		status := router.Status
		if status == "" {
			status = "enabled"
		}
		routes = append(routes, traefikRouteView{
			Name: router.Name, Rule: router.Rule, EntryPoints: router.EntryPoints,
			Service: router.Service, Middlewares: router.Middlewares, TLS: router.TLS != nil,
			Status: status, Servers: serviceServers[router.Service],
		})
	}
	c.JSON(http.StatusOK, gin.H{"configured": true, "routes": routes, "overview": overview})
}

func (s *Server) handleTraefikLogs(c *gin.Context) {
	lines := 200
	if raw := c.Query("lines"); raw != "" {
		if parsed, err := parseUintParam(raw); err == nil && parsed > 0 {
			lines = int(parsed)
		}
	}
	if lines > 2000 {
		lines = 2000
	}
	if s.cfg.Demo {
		c.JSON(http.StatusOK, gin.H{"demo": true, "lines": demoTrafikLogs(lines), "source": "demo"})
		return
	}
	setting, err := s.store.GetTraefikSetting()
	if err != nil || strings.TrimSpace(setting.LogPath) == "" {
		c.JSON(http.StatusOK, gin.H{"configured": false, "lines": []string{}})
		return
	}
	file, err := os.Open(setting.LogPath)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "open Traefik access log: " + err.Error()})
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	buffer := make([]string, 0, lines)
	for scanner.Scan() {
		buffer = append(buffer, scanner.Text())
		if len(buffer) > lines {
			buffer = buffer[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "read Traefik access log: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"configured": true, "lines": buffer, "source": setting.LogPath})
}

func demoTrafikRoutes() []traefikRouteView {
	return []traefikRouteView{
		{Name: "demo-web@docker", Rule: "Host(`demo.hkvps.xyz`)", EntryPoints: []string{"websecure"}, Service: "demo-web@docker", Middlewares: []string{"secure-headers@docker"}, TLS: true, Status: "enabled", Servers: []traefikServer{{URL: "http://10.0.0.12:8090", Status: "UP"}}},
		{Name: "api-web@docker", Rule: "Host(`api.demo.hkvps.xyz`) && PathPrefix(`/v1`)", EntryPoints: []string{"websecure"}, Service: "api-web@docker", Middlewares: []string{"rate-limit@docker", "secure-headers@docker"}, TLS: true, Status: "enabled", Servers: []traefikServer{{URL: "http://10.0.0.12:8080", Status: "UP"}, {URL: "http://10.0.0.13:8080", Status: "UP"}}},
		{Name: "console@docker", Rule: "Host(`console.demo.hkvps.xyz`)", EntryPoints: []string{"websecure"}, Service: "console@docker", Middlewares: []string{"auth@docker"}, TLS: true, Status: "enabled", Servers: []traefikServer{{URL: "http://10.0.0.14:3000", Status: "UP"}}},
	}
}

func demoTrafikLogs(lines int) []string {
	now := time.Now().UTC()
	rows := make([]string, 0, lines)
	for i := 0; i < lines; i++ {
		status := 200
		if i%17 == 0 {
			status = 404
		}
		if i%41 == 0 {
			status = 502
		}
		rows = append(rows, fmt.Sprintf(`{"time":"%s","requestMethod":"GET","requestPath":"/api/demo","status":%d,"duration":%d,"routerName":"demo-web@docker","serviceName":"demo-web@docker"}`,
			now.Add(-time.Duration(lines-i)*time.Second).Format(time.RFC3339), status, 8+i%120))
	}
	return rows
}
