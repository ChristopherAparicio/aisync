package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// delegateToolNames is the set of tool call names that indicate a sub-agent delegation.
// When a session's messages contain one of these tools, aisync looks for a referenced
// session_id in the tool input/output and creates a delegated_to link automatically.
var delegateToolNames = map[string]bool{
	"delegate":     true,
	"ask_subagent": true,
	"run_subagent": true,
	"subagent":     true,
	"computer_use": true, // Anthropic computer_use sometimes references child sessions
}

// ── Ingest ──

// IngestMessage is a lightweight message format for the ingest endpoint.
// It mirrors session.Message but with simpler types for external callers.
type IngestMessage struct {
	Role         string           `json:"role"`                    // "user", "assistant", "system"
	Content      string           `json:"content"`                 // message text
	Model        string           `json:"model,omitempty"`         // e.g. "qwen3-coder:30b"
	Thinking     string           `json:"thinking,omitempty"`      // extended thinking content
	ToolCalls    []IngestToolCall `json:"tool_calls,omitempty"`    // tool invocations
	InputTokens  int              `json:"input_tokens,omitempty"`  // per-message token count
	OutputTokens int              `json:"output_tokens,omitempty"` // per-message token count
}

// IngestToolCall is a lightweight tool call for the ingest endpoint.
type IngestToolCall struct {
	Name       string `json:"name"`                  // e.g. "bash", "memory", "delegate"
	Input      string `json:"input"`                 // tool input (command, query, etc.)
	Output     string `json:"output,omitempty"`      // tool output
	State      string `json:"state,omitempty"`       // "completed", "error", etc. (default: "completed")
	DurationMs int    `json:"duration_ms,omitempty"` // execution time in milliseconds
}

// IngestRequest contains the data for a session pushed by an external client.
// This is the simplest path to store a session — no provider detection, no
// file-system reads, no format parsing.
type IngestRequest struct {
	Provider               string          `json:"provider"`                            // REQUIRED: "parlay", "ollama", etc.
	Messages               []IngestMessage `json:"messages"`                            // REQUIRED: at least 1 message
	Agent                  string          `json:"agent,omitempty"`                     // e.g. "jarvis"; defaults to provider name
	ProjectPath            string          `json:"project_path,omitempty"`              // root of the project
	Branch                 string          `json:"branch,omitempty"`                    // git branch
	Summary                string          `json:"summary,omitempty"`                   // one-line description
	SessionID              string          `json:"session_id,omitempty"`                // optional; auto-generated if empty
	RemoteURL              string          `json:"remote_url,omitempty"`                // git remote URL (e.g. "github.com/org/repo"); normalized on store
	DelegatedFromSessionID string          `json:"delegated_from_session_id,omitempty"` // if set, creates a delegated_from link to this session
}

// IngestResult is returned after a successful ingest.
type IngestResult struct {
	SessionID session.ID           `json:"session_id"`
	Provider  session.ProviderName `json:"provider"`
}

// Ingest stores a session pushed by an external client without provider detection.
func (s *SessionService) Ingest(_ context.Context, req IngestRequest) (*IngestResult, error) {
	// Validate provider.
	providerName, err := session.ParseProviderName(req.Provider)
	if err != nil {
		return nil, fmt.Errorf("invalid provider: %w", err)
	}

	// Validate messages.
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("at least one message is required")
	}

	// Build session.
	now := time.Now().UTC()
	sess := &session.Session{
		Provider:    providerName,
		Agent:       req.Agent,
		Branch:      req.Branch,
		ProjectPath: req.ProjectPath,
		Summary:     req.Summary,
		StorageMode: session.StorageModeFull,
		CreatedAt:   now,
		ExportedAt:  now,
		ExportedBy:  "ingest",
		Version:     1,
	}

	// ID: use provided or generate.
	if req.SessionID != "" {
		sess.ID = session.ID(req.SessionID)
	} else {
		sess.ID = session.NewID()
	}

	// Agent: default to provider name.
	if sess.Agent == "" {
		sess.Agent = string(providerName)
	}

	// Remote URL: normalize if provided, else try to detect from git.
	if req.RemoteURL != "" {
		sess.RemoteURL = NormalizeRemoteURL(req.RemoteURL)
	} else {
		sess.RemoteURL = s.resolveRemoteURL()
	}

	// Convert messages.
	var totalInput, totalOutput int
	for i, m := range req.Messages {
		msg := session.Message{
			ID:           fmt.Sprintf("msg-%d", i+1),
			Role:         session.MessageRole(m.Role),
			Content:      m.Content,
			Model:        m.Model,
			Thinking:     m.Thinking,
			Timestamp:    now,
			InputTokens:  m.InputTokens,
			OutputTokens: m.OutputTokens,
		}

		totalInput += m.InputTokens
		totalOutput += m.OutputTokens

		for j, tc := range m.ToolCalls {
			state := session.ToolState(tc.State)
			if state == "" {
				state = session.ToolStateCompleted
			}
			msg.ToolCalls = append(msg.ToolCalls, session.ToolCall{
				ID:         fmt.Sprintf("tc-%d-%d", i+1, j+1),
				Name:       tc.Name,
				Input:      tc.Input,
				Output:     tc.Output,
				State:      state,
				DurationMs: tc.DurationMs,
			})
		}

		sess.Messages = append(sess.Messages, msg)
	}

	// Token usage.
	sess.TokenUsage = session.TokenUsage{
		InputTokens:  totalInput,
		OutputTokens: totalOutput,
		TotalTokens:  totalInput + totalOutput,
	}

	// Resolve owner identity (optional — nil git is safe).
	sess.OwnerID = s.resolveOwner()

	// Secret scanning (optional — nil scanner is safe).
	if s.scanner != nil {
		_ = s.scanner.ScanSession(sess)
	}

	// Stamp denormalized costs before persisting.
	s.stampCosts(sess)

	// Persist.
	if err := s.store.Save(sess); err != nil {
		return nil, fmt.Errorf("storing ingested session: %w", err)
	}
	s.stampAnalytics(sess)

	// Post-capture hook: extract events, classify errors, fire webhooks, etc.
	// Non-blocking: errors are swallowed (same contract as Capture).
	if s.postCapture != nil {
		s.postCapture(sess)
	}

	// Delegate detection: creates session-to-session links non-blocking.
	// Errors are silently swallowed — link creation is best-effort.
	s.detectAndLinkDelegation(sess, req.DelegatedFromSessionID)

	return &IngestResult{
		SessionID: sess.ID,
		Provider:  providerName,
	}, nil
}

// detectAndLinkDelegation creates session-to-session delegation links after ingest.
// Two mechanisms are supported:
//
//  1. Explicit: if delegatedFromID is non-empty, a bidirectional
//     delegated_from / delegated_to link is created immediately.
//
//  2. Heuristic: the session's tool calls are scanned for known delegation
//     tool names (e.g. "delegate", "ask_subagent"). If the tool's input or
//     output JSON contains a "session_id" field that matches a known session,
//     a delegated_to link is created from this session to the target.
//
// All errors are silently dropped — this is best-effort enrichment.
func (s *SessionService) detectAndLinkDelegation(sess *session.Session, delegatedFromID string) {
	// 1. Explicit caller-supplied parent session.
	if delegatedFromID != "" {
		parentID := session.ID(delegatedFromID)
		link := session.SessionLink{
			ID:              session.NewID(),
			SourceSessionID: sess.ID,
			TargetSessionID: parentID,
			LinkType:        session.SessionLinkDelegatedFrom,
			Description:     "auto-detected via ingest DelegatedFromSessionID",
		}
		_ = s.store.LinkSessions(link) // errors silently ignored
	}

	// 2. Heuristic scan of tool calls for delegation references.
	for _, msg := range sess.Messages {
		for _, tc := range msg.ToolCalls {
			if !delegateToolNames[strings.ToLower(tc.Name)] {
				continue
			}
			// Try to find a session_id in the tool's input or output.
			targetID := extractSessionID(tc.Input)
			if targetID == "" {
				targetID = extractSessionID(tc.Output)
			}
			if targetID == "" || session.ID(targetID) == sess.ID {
				continue
			}
			link := session.SessionLink{
				ID:              session.NewID(),
				SourceSessionID: sess.ID,
				TargetSessionID: session.ID(targetID),
				LinkType:        session.SessionLinkDelegatedTo,
				Description:     fmt.Sprintf("auto-detected via tool call %q", tc.Name),
			}
			_ = s.store.LinkSessions(link) // errors silently ignored
		}
	}
}

// extractSessionID looks for a "session_id" key in a JSON string.
// Returns the value if found, empty string otherwise.
func extractSessionID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw[0] != '{' {
		return ""
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return ""
	}
	for _, key := range []string{"session_id", "sessionId", "session-id"} {
		if v, ok := obj[key]; ok {
			var s string
			if err := json.Unmarshal(v, &s); err == nil && s != "" {
				return s
			}
		}
	}
	return ""
}

// ── Session Links ──

// SessionLinkRequest contains parameters for creating a session-to-session link.
type SessionLinkRequest struct {
	SourceSessionID string `json:"source_session_id"` // REQUIRED
	TargetSessionID string `json:"target_session_id"` // REQUIRED
	LinkType        string `json:"link_type"`         // REQUIRED: "delegated_to", "related", etc.
	Description     string `json:"description,omitempty"`
}

// LinkSessions creates a bidirectional link between two sessions.
func (s *SessionService) LinkSessions(_ context.Context, req SessionLinkRequest) (*session.SessionLink, error) {
	// Validate link type.
	linkType, err := session.ParseSessionLinkType(req.LinkType)
	if err != nil {
		return nil, fmt.Errorf("invalid link type: %w", err)
	}

	// Validate session IDs.
	if req.SourceSessionID == "" || req.TargetSessionID == "" {
		return nil, fmt.Errorf("both source_session_id and target_session_id are required")
	}
	if req.SourceSessionID == req.TargetSessionID {
		return nil, fmt.Errorf("cannot link a session to itself")
	}

	link := session.SessionLink{
		ID:              session.NewID(),
		SourceSessionID: session.ID(req.SourceSessionID),
		TargetSessionID: session.ID(req.TargetSessionID),
		LinkType:        linkType,
		Description:     req.Description,
	}

	if err := s.store.LinkSessions(link); err != nil {
		return nil, fmt.Errorf("creating session link: %w", err)
	}

	return &link, nil
}

// GetLinkedSessions retrieves all session-to-session links for a given session.
func (s *SessionService) GetLinkedSessions(_ context.Context, sessionID session.ID) ([]session.SessionLink, error) {
	return s.store.GetLinkedSessions(sessionID)
}

// DeleteSessionLink removes a session-to-session link by its ID.
func (s *SessionService) DeleteSessionLink(_ context.Context, id session.ID) error {
	return s.store.DeleteSessionLink(id)
}
