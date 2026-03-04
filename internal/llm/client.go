// Package llm defines the port for language model interactions.
// Adapters live in subpackages (claude/, future: openai/, ollama/).
package llm

import "context"

// Client is the port for language model completions.
// It abstracts the underlying LLM provider (Claude CLI, OpenAI API, etc.).
//
// Current adapters:
//   - claude/ — calls the `claude` CLI binary
type Client interface {
	// Complete sends a prompt and returns the model's text response.
	// The implementation decides how to communicate with the model.
	Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
}

// CompletionRequest contains inputs for a single LLM completion.
type CompletionRequest struct {
	// SystemPrompt sets the system-level instruction for the model.
	SystemPrompt string

	// UserPrompt is the main content to send to the model.
	UserPrompt string

	// Model is an optional model identifier (e.g., "sonnet", "opus").
	// Empty means the adapter picks its default.
	Model string

	// MaxTokens limits the response length. Zero means adapter default.
	MaxTokens int
}

// CompletionResponse contains the model's output and usage metadata.
type CompletionResponse struct {
	// Content is the model's text response.
	Content string

	// Model is the actual model identifier used for this completion.
	Model string

	// InputTokens is the number of tokens in the prompt.
	InputTokens int

	// OutputTokens is the number of tokens in the response.
	OutputTokens int
}
