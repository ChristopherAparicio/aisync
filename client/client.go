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

// ── Internal HTTP helpers ──

// doGet performs a GET request and returns the response body.
func (c *Client) doGet(path string) ([]byte, error) {
	resp, err := c.httpClient.Get(c.baseURL + path)
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

	resp, err := c.httpClient.Post(c.baseURL+path, "application/json", bytes.NewReader(data))
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

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("DELETE %s: %w", path, err)
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
