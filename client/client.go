// Package client provides an HTTP client SDK for the aisync API server.
// It mirrors the endpoints exposed by `aisync serve` and is used by
// CLI commands in "client mode" (when a running server is available)
// and by external tools integrating with aisync.
//
// Usage:
//
//	c := client.New("http://127.0.0.1:8371")
//	sessions, err := c.List(client.ListOptions{All: true})
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultTimeout = 30 * time.Second

// Client is an HTTP client for the aisync API server.
type Client struct {
	baseURL    string
	httpClient *http.Client
	authToken  string // JWT Bearer token (optional)
	apiKey     string // API key (optional, alternative to token)
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient sets a custom *http.Client (useful for testing).
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		c.httpClient = hc
	}
}

// WithTimeout sets the HTTP client timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		c.httpClient.Timeout = d
	}
}

// WithAuthToken sets a JWT Bearer token for authenticated requests.
func WithAuthToken(token string) Option {
	return func(c *Client) {
		c.authToken = token
	}
}

// WithAPIKey sets an API key for authenticated requests.
func WithAPIKey(key string) Option {
	return func(c *Client) {
		c.apiKey = key
	}
}

// New creates a Client targeting the given base URL (e.g. "http://127.0.0.1:8371").
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// ── Error types ──

// APIError represents an error response from the aisync API server.
type APIError struct {
	StatusCode int    `json:"-"`
	Message    string `json:"error"`
	Code       string `json:"code,omitempty"`
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("aisync api: %s (%s, HTTP %d)", e.Message, e.Code, e.StatusCode)
	}
	return fmt.Sprintf("aisync api: %s (HTTP %d)", e.Message, e.StatusCode)
}

// IsNotFound reports whether the error is a 404 (session not found, etc.).
func IsNotFound(err error) bool {
	if apiErr, ok := err.(*APIError); ok {
		return apiErr.StatusCode == http.StatusNotFound
	}
	return false
}

// IsUnavailable reports whether the error is a 503 (service unavailable).
func IsUnavailable(err error) bool {
	if apiErr, ok := err.(*APIError); ok {
		return apiErr.StatusCode == http.StatusServiceUnavailable
	}
	return false
}

// ── Health ──

// Health checks whether the aisync server is running.
func (c *Client) Health() error {
	_, err := c.doGet("/api/v1/health")
	return err
}

// IsAvailable probes the server health with a short timeout (500ms).
// Returns true if the server responds to the health check, false otherwise.
// This is used by the CLI to decide between remote and local mode.
func (c *Client) IsAvailable() bool {
	probe := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := probe.Get(c.baseURL + "/api/v1/health")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ── Internal HTTP helpers ──

// setAuthHeaders injects authentication headers if configured.
func (c *Client) setAuthHeaders(req *http.Request) {
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	} else if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
}

// doGet performs a GET request and returns the response body.
func (c *Client) doGet(path string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create GET request: %w", err)
	}
	c.setAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	return c.readResponse(resp)
}

// doPost performs a POST request with a JSON body and returns the response body.
func (c *Client) doPost(path string, body any) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create POST request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	return c.readResponse(resp)
}

// doDelete performs a DELETE request and returns the response body.
func (c *Client) doDelete(path string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodDelete, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create DELETE request: %w", err)
	}
	c.setAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("DELETE %s: %w", path, err)
	}
	defer resp.Body.Close()
	return c.readResponse(resp)
}

// doPatch performs a PATCH request with a JSON body.
func (c *Client) doPatch(path string, body any) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPatch, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create PATCH request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("PATCH %s: %w", path, err)
	}
	defer resp.Body.Close()
	return c.readResponse(resp)
}

// readResponse reads the response body and checks for API errors.
func (c *Client) readResponse(resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		apiErr := &APIError{StatusCode: resp.StatusCode}
		if jsonErr := json.Unmarshal(body, apiErr); jsonErr != nil {
			apiErr.Message = string(body)
		}
		return nil, apiErr
	}

	return body, nil
}

// decode unmarshals JSON data into v.
func decode(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
