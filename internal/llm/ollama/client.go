// Package ollama provides an llm.Client implementation that calls Ollama's API.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/llm"
)

// Client implements llm.Client by calling Ollama's /api/chat endpoint.
type Client struct {
	baseURL    string
	model      string
	httpClient *http.Client
}

// Config for the Ollama LLM client.
type Config struct {
	BaseURL string        // default: http://localhost:11434
	Model   string        // default: qwen3.5:35b
	Timeout time.Duration // default: 120s
}

// New creates a new Ollama LLM client.
func New(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://localhost:11434"
	}
	if cfg.Model == "" {
		cfg.Model = "qwen3.5:35b"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Minute // local models on single GPU can be very slow
	}
	return &Client{
		baseURL: cfg.BaseURL,
		model:   cfg.Model,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

// Complete sends a chat completion request to Ollama.
func (c *Client) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	messages := []chatMessage{}

	if req.SystemPrompt != "" {
		messages = append(messages, chatMessage{
			Role:    "system",
			Content: req.SystemPrompt,
		})
	}
	messages = append(messages, chatMessage{
		Role:    "user",
		Content: req.UserPrompt,
	})

	model := c.model
	if req.Model != "" {
		model = req.Model
	}

	body := chatRequest{
		Model:    model,
		Messages: messages,
		Stream:   false,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/chat", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &llm.CompletionResponse{
		Content:      chatResp.Message.Content,
		Model:        model,
		InputTokens:  chatResp.PromptEvalCount,
		OutputTokens: chatResp.EvalCount,
	}, nil
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	PromptEvalCount int `json:"prompt_eval_count"`
	EvalCount       int `json:"eval_count"`
}
