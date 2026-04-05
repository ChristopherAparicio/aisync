// Package webhook implements a generic HTTP webhook notification channel.
// It POSTs JSON payloads to configured URLs, compatible with any webhook receiver.
package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/notification"
)

// ClientConfig configures the generic webhook adapter.
type ClientConfig struct {
	// URL is the target webhook URL for HTTP POST.
	URL string

	// Secret is an optional shared secret for HMAC signature (future).
	Secret string

	// Timeout for HTTP requests (default: 10s).
	Timeout time.Duration

	// MaxRetries is the number of retry attempts on failure (default: 1).
	MaxRetries int
}

// Client implements notification.Channel for generic HTTP webhooks.
type Client struct {
	url        string
	secret     string
	httpClient *http.Client
	maxRetries int
}

// NewClient creates a generic webhook channel adapter.
// Returns nil if no URL is configured.
func NewClient(cfg ClientConfig) *Client {
	if cfg.URL == "" {
		return nil
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 1
	}
	return &Client{
		url:        cfg.URL,
		secret:     cfg.Secret,
		httpClient: &http.Client{Timeout: timeout},
		maxRetries: maxRetries,
	}
}

// Name returns the adapter identifier.
func (c *Client) Name() string { return "webhook" }

// Send delivers a rendered message to the webhook URL.
// The recipient is ignored — all messages go to the configured URL.
func (c *Client) Send(_ notification.Recipient, msg notification.RenderedMessage) error {
	if c == nil {
		return nil
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}

		req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(msg.Body))
		if err != nil {
			return fmt.Errorf("webhook: creating request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "aisync-notification/1.0")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		_ = resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}

	return fmt.Errorf("webhook: delivery failed after %d attempts: %w", c.maxRetries+1, lastErr)
}

// Formatter renders notification events as JSON payloads for generic webhooks.
type Formatter struct{}

// NewFormatter creates a generic webhook formatter.
func NewFormatter() *Formatter {
	return &Formatter{}
}

// Format renders an event into a JSON payload.
func (f *Formatter) Format(event notification.Event) (notification.RenderedMessage, error) {
	body, err := json.Marshal(event)
	if err != nil {
		return notification.RenderedMessage{}, fmt.Errorf("webhook: marshal: %w", err)
	}
	return notification.RenderedMessage{
		Body:         body,
		FallbackText: fmt.Sprintf("%s notification", event.Type),
	}, nil
}
