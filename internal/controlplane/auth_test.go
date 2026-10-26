package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestLoginJWTAndProtectedAPIs(t *testing.T) {
	server, _ := newDeployTestServer(t)

	user, err := server.store.GetAdminUserByUsername("admin")
	if err != nil {
		t.Fatalf("bootstrap admin missing: %v", err)
	}
	if user.PasswordHash == "admin123" || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("admin123")) != nil {
		t.Fatal("bootstrap password is not stored as a valid bcrypt hash")
	}

	recorder := apiCallNoAuth(t, server, http.MethodGet, "/api/nodes", "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", recorder.Code)
	}

	recorder = apiCallNoAuth(t, server, http.MethodPost, "/api/login",
		`{"username":"admin","password":"wrong-password"}`)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, want 401", recorder.Code)
	}

	recorder = apiCallNoAuth(t, server, http.MethodPost, "/api/login",
		`{"username":"admin","password":"admin123"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var login struct {
		Token string `json:"token"`
		User  struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &login); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if login.Token == "" || login.User.Username != "admin" {
		t.Fatalf("unexpected login response: %+v", login)
	}

	recorder = apiCallNoAuthWithToken(t, server, http.MethodGet, "/api/nodes", "", login.Token)
	if recorder.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = apiCallNoAuthWithToken(t, server, http.MethodGet, "/api/nodes", "", "invalid-token")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("invalid JWT status = %d, want 401", recorder.Code)
	}
}

func TestChangeAdminPassword(t *testing.T) {
	server, _ := newDeployTestServer(t)
	token := testAuthToken(t, server)

	recorder := apiCallNoAuthWithToken(t, server, http.MethodPost, "/api/admin/password",
		`{"current_password":"wrong","new_password":"new-password-42"}`, token)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current password status = %d", recorder.Code)
	}

	recorder = apiCallNoAuthWithToken(t, server, http.MethodPost, "/api/admin/password",
		`{"current_password":"admin123","new_password":"new-password-42"}`, token)
	if recorder.Code != http.StatusOK {
		t.Fatalf("change password status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = apiCallNoAuth(t, server, http.MethodPost, "/api/login",
		`{"username":"admin","password":"admin123"}`)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("old password status = %d, want 401", recorder.Code)
	}
	recorder = apiCallNoAuth(t, server, http.MethodPost, "/api/login",
		`{"username":"admin","password":"new-password-42"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("new password status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestSSEAcceptsAuthorizationHeader(t *testing.T) {
	server, _ := newDeployTestServer(t)
	token := testAuthToken(t, server)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.Handler().ServeHTTP(recorder, req)
		close(done)
	}()
	time.Sleep(80 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE handler did not stop after context cancellation")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("SSE status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "event: telemetry_snapshot") {
		t.Fatalf("SSE body missing snapshot: %s", recorder.Body.String())
	}

	recorder = apiCallNoAuth(t, server, http.MethodGet, "/api/nodes?access_token="+token, "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("query token status = %d, want 401", recorder.Code)
	}
}

func apiCallNoAuth(t *testing.T, server *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return apiCallNoAuthWithToken(t, server, method, path, body, "")
}

func apiCallNoAuthWithToken(t *testing.T, server *Server, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	return recorder
}
