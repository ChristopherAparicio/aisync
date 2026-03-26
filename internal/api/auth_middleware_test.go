package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/api"
	"github.com/ChristopherAparicio/aisync/internal/auth"
	"github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
)

// doRequestWithAuth is like doRequest but adds an Authorization: Bearer header.
func doRequestWithAuth(t *testing.T, handler http.Handler, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req := httptest.NewRequest(method, path, reqBody)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

// newTestServerWithAuth creates an API server with auth enabled.
func newTestServerWithAuth(t *testing.T) http.Handler {
	t.Helper()
	store := testutil.MustOpenStore(t)

	sessionSvc := service.NewSessionService(service.SessionServiceConfig{
		Store:     store,
		Registry:  provider.NewRegistry(),
		Converter: converter.New(),
	})

	authSvc := auth.NewService(auth.ServiceConfig{
		Store:     store,
		JWTSecret: "test-secret-at-least-32-chars-long!!",
		TokenTTL:  1 * time.Hour,
	})

	srv := api.New(api.Config{
		SessionService: sessionSvc,
		AuthService:    authSvc,
		Addr:           "127.0.0.1:0",
	})

	return srv.Handler()
}

// TestAuthMiddleware_OpenWhenNoUsers verifies that when auth is enabled but
// no users have been registered, all API endpoints are accessible without
// credentials ("open until first registration" dev mode).
func TestAuthMiddleware_OpenWhenNoUsers(t *testing.T) {
	handler := newTestServerWithAuth(t)

	// List sessions should work without any credentials.
	resp := doRequest(t, handler, "GET", "/api/v1/sessions", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 when no users exist (open access), got %d: %s", resp.Code, resp.Body.String())
	}
}

// TestAuthMiddleware_EnforcedAfterRegistration verifies that once a user
// registers, subsequent unauthenticated requests are rejected.
func TestAuthMiddleware_EnforcedAfterRegistration(t *testing.T) {
	handler := newTestServerWithAuth(t)

	// Before registration: should be open.
	resp := doRequest(t, handler, "GET", "/api/v1/sessions", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 before registration, got %d", resp.Code)
	}

	// Register the first user.
	regResp := doRequest(t, handler, "POST", "/api/v1/auth/register", map[string]string{
		"username": "admin",
		"password": "test-password-123",
	})
	if regResp.Code != http.StatusCreated {
		t.Fatalf("registration failed: %d: %s", regResp.Code, regResp.Body.String())
	}

	// Extract the token.
	var authResult struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(regResp.Body).Decode(&authResult); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	if authResult.Token == "" {
		t.Fatal("expected a token from registration")
	}

	// After registration: unauthenticated request should be rejected.
	resp = doRequest(t, handler, "GET", "/api/v1/sessions", nil)
	if resp.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 after registration without credentials, got %d: %s", resp.Code, resp.Body.String())
	}

	// With valid token: should succeed.
	resp = doRequestWithAuth(t, handler, "GET", "/api/v1/sessions", nil, authResult.Token)
	if resp.Code != http.StatusOK {
		t.Errorf("expected 200 with valid token, got %d: %s", resp.Code, resp.Body.String())
	}
}

// TestAuthMiddleware_PublicRoutesAlwaysAccessible verifies that health and
// auth endpoints are accessible regardless of auth state.
func TestAuthMiddleware_PublicRoutesAlwaysAccessible(t *testing.T) {
	handler := newTestServerWithAuth(t)

	// Register a user to activate auth enforcement.
	doRequest(t, handler, "POST", "/api/v1/auth/register", map[string]string{
		"username": "admin",
		"password": "test-password-123",
	})

	// Health is always public.
	resp := doRequest(t, handler, "GET", "/api/v1/health", nil)
	if resp.Code != http.StatusOK {
		t.Errorf("health should always be public, got %d", resp.Code)
	}

	// Login is always public.
	resp = doRequest(t, handler, "POST", "/api/v1/auth/login", map[string]string{
		"username": "admin",
		"password": "test-password-123",
	})
	if resp.Code != http.StatusOK {
		t.Errorf("login should always be public, got %d: %s", resp.Code, resp.Body.String())
	}

	// Register is always public (will fail with duplicate user, but not 401).
	resp = doRequest(t, handler, "POST", "/api/v1/auth/register", map[string]string{
		"username": "admin",
		"password": "test-password-123",
	})
	// Should be a conflict/error, not 401.
	if resp.Code == http.StatusUnauthorized {
		t.Error("register should not return 401, it's a public route")
	}
}

// TestAuthMiddleware_NilAuthSvc verifies that when authSvc is nil (auth
// not configured), all requests pass through without credentials.
func TestAuthMiddleware_NilAuthSvc(t *testing.T) {
	// Standard test server has no auth service.
	handler, _ := newTestServer(t)

	resp := doRequest(t, handler, "GET", "/api/v1/sessions", nil)
	if resp.Code != http.StatusOK {
		t.Errorf("expected 200 when auth is disabled, got %d", resp.Code)
	}
}
