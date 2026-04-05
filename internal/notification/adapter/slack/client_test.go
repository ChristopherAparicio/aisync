package slack

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/notification"
)

func TestNewClient_NilWhenNoConfig(t *testing.T) {
	c := NewClient(ClientConfig{})
	if c != nil {
		t.Error("expected nil client when no URL or token")
	}
}

func TestNewClient_WebhookMode(t *testing.T) {
	c := NewClient(ClientConfig{WebhookURL: "https://hooks.slack.com/test"})
	if c == nil {
		t.Fatal("expected non-nil client with webhook URL")
	}
	if c.Name() != "slack" {
		t.Errorf("Name() = %q, want slack", c.Name())
	}
}

func TestClient_SendViaWebhook(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := NewClient(ClientConfig{WebhookURL: srv.URL})

	err := c.Send(
		notification.Recipient{Type: notification.RecipientChannel, Target: "#test"},
		notification.RenderedMessage{Body: []byte(`{"text":"hello"}`)},
	)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if gotBody != `{"text":"hello"}` {
		t.Errorf("body = %q, want {\"text\":\"hello\"}", gotBody)
	}
}

func TestClient_SendViaWebhook_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	c := NewClient(ClientConfig{WebhookURL: srv.URL})

	err := c.Send(
		notification.Recipient{},
		notification.RenderedMessage{Body: []byte(`{}`)},
	)
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %v, want to contain 500", err)
	}
}

func TestClient_SendViaBot(t *testing.T) {
	c := NewClient(ClientConfig{BotToken: "xoxb-test-token"})
	// We can't easily test the real Slack API URL, but we verify bot mode is created
	if c == nil {
		t.Fatal("expected non-nil client with bot token")
	}
	if c.botToken != "xoxb-test-token" {
		t.Errorf("botToken = %q, want xoxb-test-token", c.botToken)
	}
}

func TestClient_NilSend(t *testing.T) {
	var c *Client
	err := c.Send(notification.Recipient{}, notification.RenderedMessage{})
	if err != nil {
		t.Errorf("nil client Send() error = %v, want nil", err)
	}
}

func TestInjectChannel(t *testing.T) {
	input := `{"blocks":[],"text":"hello"}`
	output := string(injectChannel([]byte(input), "C123"))
	expected := `{"channel":"C123","blocks":[],"text":"hello"}`
	if output != expected {
		t.Errorf("injectChannel = %q, want %q", output, expected)
	}
}

func TestInjectChannel_EmptyBody(t *testing.T) {
	output := string(injectChannel([]byte(""), "C123"))
	if output != "" {
		t.Errorf("injectChannel(empty) = %q, want empty", output)
	}
}

// ── Formatter tests ──

func TestFormatter_BudgetAlert(t *testing.T) {
	f := NewFormatter()

	msg, err := f.Format(notification.Event{
		Type:    notification.EventBudgetAlert,
		Project: "org/repo",
		Data: notification.BudgetAlertData{
			AlertType:  "monthly",
			AlertLevel: "warning",
			Spent:      120,
			Limit:      150,
			Percent:    80,
			Projected:  185,
		},
	})
	if err != nil {
		t.Fatalf("Format() error = %v", err)
	}

	if len(msg.Body) == 0 {
		t.Fatal("empty body")
	}

	// Verify it's valid JSON
	var payload map[string]any
	if err := json.Unmarshal(msg.Body, &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Should have blocks and text
	if _, ok := payload["blocks"]; !ok {
		t.Error("missing blocks in payload")
	}
	if _, ok := payload["text"]; !ok {
		t.Error("missing text (fallback) in payload")
	}

	if msg.FallbackText == "" {
		t.Error("empty fallback text")
	}
}

func TestFormatter_DailyDigest(t *testing.T) {
	f := NewFormatter()

	msg, err := f.Format(notification.Event{
		Type:    notification.EventDailyDigest,
		Project: "org/repo",
		Data: notification.DigestData{
			Period:       "2026-04-01",
			SessionCount: 14,
			TotalTokens:  380000,
			TotalCost:    5.20,
			ErrorCount:   3,
			Owners: []notification.DigestOwnerData{
				{Name: "john", Kind: "human", SessionCount: 8, TotalCost: 3.10, ErrorCount: 1},
				{Name: "claude-reviewer", Kind: "machine", SessionCount: 2, TotalCost: 0.70},
			},
		},
	})
	if err != nil {
		t.Fatalf("Format() error = %v", err)
	}

	body := string(msg.Body)
	if !strings.Contains(body, "Daily") {
		t.Error("missing 'Daily' in body")
	}
	if !strings.Contains(body, "john") {
		t.Error("missing owner 'john' in body")
	}
	if !strings.Contains(body, "robot_face") {
		t.Error("missing robot emoji for machine account")
	}
}

func TestFormatter_WeeklyReport(t *testing.T) {
	f := NewFormatter()

	msg, err := f.Format(notification.Event{
		Type:    notification.EventWeeklyReport,
		Project: "org/repo",
		Data: notification.DigestData{
			Period:       "W14 2026",
			SessionCount: 67,
			TotalCost:    45.20,
			SessionDelta: "+12%",
			CostDelta:    "-5%",
			ErrorDelta:   "-38%",
			Verdict:      "improving",
		},
	})
	if err != nil {
		t.Fatalf("Format() error = %v", err)
	}

	body := string(msg.Body)
	if !strings.Contains(body, "Weekly") {
		t.Error("missing 'Weekly' in body")
	}
	if !strings.Contains(body, "improving") {
		t.Error("missing verdict in body")
	}
}

func TestFormatter_SessionCaptured(t *testing.T) {
	f := NewFormatter()

	msg, err := f.Format(notification.Event{
		Type:    notification.EventSessionCaptured,
		Project: "org/repo",
		Data: notification.SessionCapturedData{
			SessionID: "abc123def456",
			Agent:     "claude",
			Branch:    "feature/auth",
			Summary:   "Implement OAuth2",
			Tokens:    150000,
		},
	})
	if err != nil {
		t.Fatalf("Format() error = %v", err)
	}

	body := string(msg.Body)
	if !strings.Contains(body, "Session Captured") {
		t.Error("missing 'Session Captured' in body")
	}
}

func TestFormatter_PersonalDigest(t *testing.T) {
	f := NewFormatter()

	msg, err := f.Format(notification.Event{
		Type: notification.EventPersonalDaily,
		Data: notification.PersonalDigestData{
			Period:       "Apr 1",
			OwnerName:    "John",
			SessionCount: 8,
			TotalCost:    3.20,
			TeamAvgCost:  2.78,
		},
	})
	if err != nil {
		t.Fatalf("Format() error = %v", err)
	}

	body := string(msg.Body)
	if !strings.Contains(body, "Your AI Sessions") {
		t.Error("missing personal header in body")
	}
	if !strings.Contains(body, "Team Average") {
		t.Error("missing team comparison in body")
	}
}

func TestFormatter_DashboardLink(t *testing.T) {
	f := NewFormatter()

	msg, err := f.Format(notification.Event{
		Type:         notification.EventBudgetAlert,
		DashboardURL: "http://localhost:8371",
		Data: notification.BudgetAlertData{
			AlertType: "monthly", Spent: 100, Limit: 200, Percent: 50,
		},
	})
	if err != nil {
		t.Fatalf("Format() error = %v", err)
	}

	body := string(msg.Body)
	if !strings.Contains(body, "View in Dashboard") {
		t.Error("missing dashboard link in body")
	}
}

func TestFormatter_InvalidData(t *testing.T) {
	f := NewFormatter()

	// Pass wrong data type
	msg, err := f.Format(notification.Event{
		Type: notification.EventBudgetAlert,
		Data: "not a struct",
	})
	if err != nil {
		t.Fatalf("Format() error = %v (should not fail, just degrade)", err)
	}
	if !strings.Contains(string(msg.Body), "invalid data") {
		t.Error("expected 'invalid data' fallback in body")
	}
}

func TestFormatter_ErrorSpike(t *testing.T) {
	f := NewFormatter()

	msg, err := f.Format(notification.Event{
		Type:    notification.EventErrorSpike,
		Project: "org/repo",
		Data: notification.ErrorSpikeData{
			ErrorCount:    12,
			WindowMinutes: 10,
			ErrorTypes:    []string{"timeout", "permission_denied"},
		},
	})
	if err != nil {
		t.Fatalf("Format() error = %v", err)
	}

	body := string(msg.Body)
	if !strings.Contains(body, "Error Spike") {
		t.Error("missing 'Error Spike' in body")
	}
	if !strings.Contains(body, "timeout") {
		t.Error("missing error type in body")
	}
}

func TestFormatter_Recommendations(t *testing.T) {
	f := NewFormatter()

	msg, err := f.Format(notification.Event{
		Type:    notification.EventRecommendation,
		Project: "org/repo",
		Data: notification.RecommendationData{
			TotalCount: 5,
			HighCount:  2,
			Items: []notification.RecommendationItem{
				{Type: "agent_error", Priority: "high", Icon: "⚠️", Title: "Agent X has high error rate", Message: "Fix it.", Impact: "5 errors"},
				{Type: "context_saturation", Priority: "high", Icon: "🔴", Title: "80% context", Message: "Split tasks."},
				{Type: "skill_ghost", Priority: "medium", Icon: "👻", Title: "Unused skill", Message: "Remove it.", Impact: "~2K tokens/session"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Format() error = %v", err)
	}

	body := string(msg.Body)
	if !strings.Contains(body, "Recommendations") {
		t.Error("missing 'Recommendations' in body")
	}
	if !strings.Contains(body, "org/repo") {
		t.Error("missing project name in body")
	}
	if !strings.Contains(body, "5 recommendations") {
		t.Error("missing total count in body")
	}
	if !strings.Contains(body, "2 high priority") {
		t.Error("missing high count in body")
	}
	if !strings.Contains(body, "Agent X") {
		t.Error("missing recommendation title in body")
	}
	if !strings.Contains(body, "5 errors") {
		t.Error("missing impact in body")
	}

	if msg.FallbackText == "" {
		t.Error("empty fallback text")
	}
	if !strings.Contains(msg.FallbackText, "5 items") {
		t.Errorf("fallback text %q should contain '5 items'", msg.FallbackText)
	}
}

func TestFormatter_Recommendations_InvalidData(t *testing.T) {
	f := NewFormatter()

	msg, err := f.Format(notification.Event{
		Type: notification.EventRecommendation,
		Data: "not recommendation data",
	})
	if err != nil {
		t.Fatalf("Format() error = %v", err)
	}

	body := string(msg.Body)
	if !strings.Contains(body, "invalid data") {
		t.Error("missing 'invalid data' marker in body")
	}
}

func TestFormatter_Recommendations_EmptyProject(t *testing.T) {
	f := NewFormatter()

	msg, err := f.Format(notification.Event{
		Type: notification.EventRecommendation,
		Data: notification.RecommendationData{
			TotalCount: 1,
			HighCount:  1,
			Items: []notification.RecommendationItem{
				{Type: "budget_warning", Priority: "high", Icon: "💸", Title: "Over budget", Message: "Check."},
			},
		},
	})
	if err != nil {
		t.Fatalf("Format() error = %v", err)
	}

	body := string(msg.Body)
	if !strings.Contains(body, "All Projects") {
		t.Error("missing 'All Projects' fallback for empty project")
	}
}

func TestProgressBar(t *testing.T) {
	tests := []struct {
		pct  int
		want string
	}{
		{0, "░░░░░░░░░░░░░░░░░░░░"},
		{50, "██████████░░░░░░░░░░"},
		{100, "████████████████████"},
		{25, "█████░░░░░░░░░░░░░░░"},
	}
	for _, tt := range tests {
		got := progressBar(tt.pct)
		if got != tt.want {
			t.Errorf("progressBar(%d) = %q, want %q", tt.pct, got, tt.want)
		}
	}
}

// ── LookupByEmail Tests ──

func TestLookupByEmail_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if !strings.Contains(r.Header.Get("Authorization"), "Bearer xoxb-test") {
			t.Error("missing bot token in Authorization header")
		}
		email := r.URL.Query().Get("email")
		if email != "alice@example.com" {
			t.Errorf("email param = %q, want alice@example.com", email)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"ok": true,
			"user": {
				"id": "U001ABC",
				"real_name": "Alice Smith",
				"profile": {
					"display_name": "alice"
				}
			}
		}`))
	}))
	defer srv.Close()

	c := &Client{
		botToken:   "xoxb-test",
		httpClient: srv.Client(),
	}
	// Override the API URL by using the test server URL.
	// We need to call lookupByEmailWithURL instead — but since we can't change
	// the hardcoded URL, we use the test server's HTTP client transport.
	// Actually, the simplest approach: override httpClient to redirect to our test server.
	c.httpClient = &http.Client{
		Transport: &rewriteTransport{base: srv.Client().Transport, targetURL: srv.URL},
	}

	user, err := c.LookupByEmail("alice@example.com")
	if err != nil {
		t.Fatalf("LookupByEmail() error = %v", err)
	}
	if user == nil {
		t.Fatal("expected non-nil user")
	}
	if user.ID != "U001ABC" {
		t.Errorf("ID = %q, want U001ABC", user.ID)
	}
	if user.Name != "alice" {
		t.Errorf("Name = %q, want alice", user.Name)
	}
	if user.RealName != "Alice Smith" {
		t.Errorf("RealName = %q, want Alice Smith", user.RealName)
	}
}

func TestLookupByEmail_UserNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok": false, "error": "users_not_found"}`))
	}))
	defer srv.Close()

	c := &Client{
		botToken:   "xoxb-test",
		httpClient: &http.Client{Transport: &rewriteTransport{base: srv.Client().Transport, targetURL: srv.URL}},
	}

	user, err := c.LookupByEmail("nobody@example.com")
	if err != nil {
		t.Fatalf("LookupByEmail() error = %v, want nil for not found", err)
	}
	if user != nil {
		t.Errorf("expected nil user for not found, got %+v", user)
	}
}

func TestLookupByEmail_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok": false, "error": "invalid_auth"}`))
	}))
	defer srv.Close()

	c := &Client{
		botToken:   "xoxb-bad-token",
		httpClient: &http.Client{Transport: &rewriteTransport{base: srv.Client().Transport, targetURL: srv.URL}},
	}

	user, err := c.LookupByEmail("alice@example.com")
	if err == nil {
		t.Fatal("expected error for API error response")
	}
	if !strings.Contains(err.Error(), "invalid_auth") {
		t.Errorf("error = %v, want to contain invalid_auth", err)
	}
	if user != nil {
		t.Errorf("expected nil user on error, got %+v", user)
	}
}

func TestLookupByEmail_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("server error"))
	}))
	defer srv.Close()

	c := &Client{
		botToken:   "xoxb-test",
		httpClient: &http.Client{Transport: &rewriteTransport{base: srv.Client().Transport, targetURL: srv.URL}},
	}

	_, err := c.LookupByEmail("alice@example.com")
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %v, want to contain 500", err)
	}
}

func TestLookupByEmail_NoBotToken(t *testing.T) {
	c := &Client{webhookURL: "https://hooks.slack.com/test"}

	_, err := c.LookupByEmail("alice@example.com")
	if err == nil {
		t.Fatal("expected error when no bot token")
	}
}

func TestLookupByEmail_NilClient(t *testing.T) {
	var c *Client
	_, err := c.LookupByEmail("alice@example.com")
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestLookupByEmail_EmptyEmail(t *testing.T) {
	c := &Client{botToken: "xoxb-test"}
	_, err := c.LookupByEmail("")
	if err == nil {
		t.Fatal("expected error for empty email")
	}
}

func TestHasBotToken(t *testing.T) {
	var nilClient *Client
	if nilClient.HasBotToken() {
		t.Error("nil client should not have bot token")
	}

	webhookOnly := &Client{webhookURL: "https://hooks.slack.com/test"}
	if webhookOnly.HasBotToken() {
		t.Error("webhook-only client should not have bot token")
	}

	withToken := &Client{botToken: "xoxb-test"}
	if !withToken.HasBotToken() {
		t.Error("client with bot token should return true")
	}
}

// rewriteTransport rewrites all request URLs to point to a test server,
// allowing us to test code that uses hardcoded API URLs (like slack.com/api/...).
type rewriteTransport struct {
	base      http.RoundTripper
	targetURL string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Rewrite the URL to point to our test server, preserving path and query.
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.targetURL, "http://")
	transport := t.base
	if transport == nil {
		transport = http.DefaultTransport
	}
	return transport.RoundTrip(req)
}
