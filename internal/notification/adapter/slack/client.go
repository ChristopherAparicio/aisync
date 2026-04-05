// Package slack implements the Slack notification channel adapter.
// It supports two modes:
//
//   - Webhook mode: POST to an Incoming Webhook URL (simple, channel-only)
//   - Bot mode: Use Slack Web API with a bot token (DMs, multi-channel)
//
// No external dependencies — uses net/http directly.
package slack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/notification"
)

// ClientConfig configures the Slack channel adapter.
type ClientConfig struct {
	// WebhookURL is the Incoming Webhook URL (for webhook mode).
	// If set, messages are POSTed to this URL.
	WebhookURL string

	// BotToken is the Slack Bot User OAuth Token (xoxb-...) for bot mode.
	// If set, messages are sent via chat.postMessage API.
	BotToken string

	// Timeout for HTTP requests (default: 10s).
	Timeout time.Duration
}

// Client implements notification.Channel for Slack.
type Client struct {
	webhookURL string
	botToken   string
	httpClient *http.Client
}

// NewClient creates a Slack channel adapter.
// Returns nil if neither webhook URL nor bot token is configured.
func NewClient(cfg ClientConfig) *Client {
	if cfg.WebhookURL == "" && cfg.BotToken == "" {
		return nil
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		webhookURL: cfg.WebhookURL,
		botToken:   cfg.BotToken,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// Name returns the adapter identifier.
func (c *Client) Name() string { return "slack" }

// Send delivers a rendered message to a Slack recipient.
func (c *Client) Send(recipient notification.Recipient, msg notification.RenderedMessage) error {
	if c == nil {
		return nil
	}

	switch {
	case c.botToken != "":
		return c.sendViaBot(recipient, msg)
	case c.webhookURL != "":
		return c.sendViaWebhook(msg)
	default:
		return fmt.Errorf("slack: no webhook URL or bot token configured")
	}
}

// sendViaWebhook POSTs the message body directly to the webhook URL.
// The body should be a valid Slack message payload (with "blocks" and/or "text").
func (c *Client) sendViaWebhook(msg notification.RenderedMessage) error {
	req, err := http.NewRequest(http.MethodPost, c.webhookURL, bytes.NewReader(msg.Body))
	if err != nil {
		return fmt.Errorf("slack webhook: creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack webhook: sending: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("slack webhook: HTTP %d: %s", resp.StatusCode, body)
	}

	return nil
}

// sendViaBot uses the Slack Web API (chat.postMessage) with a bot token.
// This supports posting to any channel or DM.
func (c *Client) sendViaBot(recipient notification.Recipient, msg notification.RenderedMessage) error {
	// For bot mode, the formatter includes "channel" in the JSON body.
	// We need to inject the target channel/user from the recipient.
	// The formatter produces {"blocks": [...], "text": "..."} without "channel".
	// We wrap it to add the channel field.
	body := injectChannel(msg.Body, recipient.Target)

	req, err := http.NewRequest(http.MethodPost, "https://slack.com/api/chat.postMessage", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slack bot: creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+c.botToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack bot: sending: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("slack bot: HTTP %d: %s", resp.StatusCode, body)
	}

	return nil
}

// injectChannel wraps a JSON body to add a "channel" field.
// Input:  {"blocks": [...], "text": "..."}
// Output: {"channel": "C123", "blocks": [...], "text": "..."}
func injectChannel(body []byte, channel string) []byte {
	if len(body) < 2 || body[0] != '{' {
		return body
	}
	// Simple injection: prepend "channel": "..." right after the opening {
	prefix := fmt.Sprintf(`{"channel":"%s",`, channel)
	return append([]byte(prefix), body[1:]...)
}

// ── Slack User Lookup ──

// SlackUser represents the subset of a Slack user profile returned by users.lookupByEmail.
type SlackUser struct {
	ID       string // Slack user ID (e.g. "U0123ABCDEF")
	Name     string // Slack display name
	RealName string // Slack full name
}

// HasBotToken reports whether the client has a bot token configured.
// The users.lookupByEmail API requires a bot token with the users:read.email scope.
func (c *Client) HasBotToken() bool {
	return c != nil && c.botToken != ""
}

// LookupByEmail resolves a Slack user by their email address using the users.lookupByEmail API.
// Requires a bot token with the users:read.email scope.
// Returns (nil, nil) if the user is not found (users_not_found error from Slack).
// Returns (nil, error) on API or network errors.
func (c *Client) LookupByEmail(email string) (*SlackUser, error) {
	if c == nil || c.botToken == "" {
		return nil, fmt.Errorf("slack: bot token required for users.lookupByEmail")
	}
	if email == "" {
		return nil, fmt.Errorf("slack: email is required")
	}

	apiURL := "https://slack.com/api/users.lookupByEmail?email=" + url.QueryEscape(email)

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("slack lookupByEmail: creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.botToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("slack lookupByEmail: sending: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return nil, fmt.Errorf("slack lookupByEmail: reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("slack lookupByEmail: HTTP %d: %s", resp.StatusCode, body)
	}

	var result lookupByEmailResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("slack lookupByEmail: parsing response: %w", err)
	}

	if !result.OK {
		// "users_not_found" is the expected error when the email doesn't match a Slack user.
		if result.Error == "users_not_found" {
			return nil, nil
		}
		return nil, fmt.Errorf("slack lookupByEmail: API error: %s", result.Error)
	}

	return &SlackUser{
		ID:       result.User.ID,
		Name:     result.User.Profile.DisplayName,
		RealName: result.User.RealName,
	}, nil
}

// lookupByEmailResponse is the JSON shape of Slack's users.lookupByEmail response.
type lookupByEmailResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	User  struct {
		ID       string `json:"id"`
		RealName string `json:"real_name"`
		Profile  struct {
			DisplayName string `json:"display_name"`
		} `json:"profile"`
	} `json:"user"`
}
