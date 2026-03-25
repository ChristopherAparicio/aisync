package opencode

import (
	"strconv"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/google/uuid"
)

// ExtractErrors extracts structured SessionError entities from OpenCode messages.
// It captures two types of errors:
//   - API-level errors: HTTP errors from the LLM provider (stored in message.data.error)
//   - Tool-level errors: tool calls with state="error"
//
// The returned errors are unclassified — they have raw data populated
// (HTTPStatus, RawError, Headers, etc.) but Category and Source need
// to be filled in by an ErrorClassifier.
func ExtractErrors(sessionID session.ID, messages []ocMessage, domainMessages []session.Message) []session.SessionError {
	var errors []session.SessionError

	for i, msg := range messages {
		// 1. Extract API-level errors (e.g. Anthropic HTTP 500).
		if msg.Error != nil {
			apiErr := extractAPIError(sessionID, msg, i)
			errors = append(errors, apiErr)
		}

		// 2. Extract tool-level errors from domain messages.
		if i < len(domainMessages) {
			for _, tc := range domainMessages[i].ToolCalls {
				if tc.State == session.ToolStateError {
					toolErr := extractToolError(sessionID, msg, domainMessages[i], tc, i)
					errors = append(errors, toolErr)
				}
			}
		}
	}

	return errors
}

// extractAPIError converts an OpenCode API error into a SessionError.
func extractAPIError(sessionID session.ID, msg ocMessage, msgIndex int) session.SessionError {
	err := session.SessionError{
		ID:           uuid.New().String(),
		SessionID:    sessionID,
		Source:       session.ErrorSourceProvider,
		MessageID:    msg.ID,
		MessageIndex: msgIndex,
		RawError:     msg.Error.Data.Message,
		HTTPStatus:   msg.Error.Data.StatusCode,
		ProviderName: resolveProviderName(msg),
		IsRetryable:  msg.Error.Data.IsRetryable,
		OccurredAt:   time.UnixMilli(msg.Time.Created),
	}

	// Copy relevant response headers.
	if len(msg.Error.Data.ResponseHeaders) > 0 {
		err.Headers = make(map[string]string, len(msg.Error.Data.ResponseHeaders))
		for k, v := range msg.Error.Data.ResponseHeaders {
			err.Headers[k] = v
		}
	}

	// Extract request ID from headers.
	if reqID, ok := msg.Error.Data.ResponseHeaders["request-id"]; ok {
		err.RequestID = reqID
	}

	// Extract upstream service time as duration.
	if svcTime, ok := msg.Error.Data.ResponseHeaders["x-envoy-upstream-service-time"]; ok {
		if ms, parseErr := strconv.Atoi(svcTime); parseErr == nil {
			err.DurationMs = ms
		}
	}

	// Include response body in raw error if the message alone is sparse.
	if msg.Error.Data.ResponseBody != "" && len(msg.Error.Data.ResponseBody) > len(msg.Error.Data.Message) {
		err.RawError = msg.Error.Data.ResponseBody
	}

	return err
}

// extractToolError converts a failed tool call into a SessionError.
func extractToolError(sessionID session.ID, ocMsg ocMessage, domainMsg session.Message, tc session.ToolCall, msgIndex int) session.SessionError {
	return session.SessionError{
		ID:           uuid.New().String(),
		SessionID:    sessionID,
		Source:       session.ErrorSourceTool,
		MessageID:    domainMsg.ID,
		MessageIndex: msgIndex,
		ToolName:     tc.Name,
		ToolCallID:   tc.ID,
		RawError:     truncate(tc.Output, 2000),
		OccurredAt:   domainMsg.Timestamp,
		DurationMs:   tc.DurationMs,
	}
}

// resolveProviderName extracts the LLM provider name from a message.
func resolveProviderName(msg ocMessage) string {
	if msg.ProviderID != "" {
		return msg.ProviderID
	}
	if msg.Model.ProviderID != "" {
		return msg.Model.ProviderID
	}
	return ""
}

// truncate limits a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
