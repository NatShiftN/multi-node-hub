package controlplane

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestACMEConfigValidationAndRedirect(t *testing.T) {
	server, _ := newDeployTestServer(t)
	base := server.cfg
	base.Domain = "nodehub.example.com"
	base.Email = "ops@example.com"
	base.CertDir = t.TempDir()
	base.HTTPAddr = ":8080"
	base.HTTPSAddr = ":8443"
	if err := base.Validate(); err != nil {
		t.Fatalf("valid TLS config rejected: %v", err)
	}

	for _, domain := range []string{
		"https://nodehub.example.com",
		"nodehub.example.com:443",
		"bad domain.example.com",
		"-bad.example.com",
	} {
		invalid := base
		invalid.Domain = domain
		if err := invalid.Validate(); err == nil {
			t.Fatalf("domain %q should be rejected", domain)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/nodes?x=1", nil)
	recorder := httptest.NewRecorder()
	acmeRedirectHandler(base.Domain).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusMovedPermanently {
		t.Fatalf("redirect status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Location"); got != "https://nodehub.example.com/api/nodes?x=1" {
		t.Fatalf("redirect location = %q", got)
	}
}
