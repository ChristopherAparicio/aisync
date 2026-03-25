package sessionevent

import (
	"encoding/json"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/google/uuid"
)

// maxFullCommandLen is the max length for the full command string stored in CommandDetail.
const maxFullCommandLen = 512

// Processor extracts structured events from a session's messages.
// It is purely deterministic — no LLM calls, no network I/O.
// It should run at capture time for every session.
type Processor struct{}

// NewProcessor creates a new event processor.
func NewProcessor() *Processor {
	return &Processor{}
}

// ExtractAll extracts all events from a session. This is the main entry point.
// It processes every message and returns a flat list of events, plus a summary.
//
// The events are extracted in message order, making the event stream
// a chronological record of what happened during the session.
func (p *Processor) ExtractAll(sess *session.Session) ([]Event, SessionEventSummary) {
	if sess == nil {
		return nil, SessionEventSummary{}
	}

	var events []Event

	// 1. Agent detection — emit once per session (even with no messages).
	events = append(events, p.extractAgentEvent(sess))

	// 2. Error events from session.Errors (before message scan, as errors
	//    may exist even when messages are empty or in compact storage mode).
	events = append(events, p.errorEvents(sess)...)

	// If no messages, return early with agent + errors.
	if len(sess.Messages) == 0 {
		summary := NewSessionEventSummary(sess.ID, events)
		return events, summary
	}

	// 2. Extract model detections from messages.
	modelEvents := p.extractModelEvents(sess)
	events = append(events, modelEvents...)

	// 3. Process each message for tool calls, skills, commands, images, errors.
	for msgIdx := range sess.Messages {
		msg := &sess.Messages[msgIdx]

		// Tool calls → tool_call events + command events + skill events.
		for tcIdx := range msg.ToolCalls {
			tc := &msg.ToolCalls[tcIdx]

			// Always emit a tool_call event.
			events = append(events, p.toolCallEvent(sess, msgIdx, msg, tc))

			// If it's a bash/shell command, also emit a command event.
			if cmdEvt := p.commandEvent(sess, msgIdx, msg, tc); cmdEvt != nil {
				events = append(events, *cmdEvt)
			}

			// If it's a skill load via tool call, emit a skill_load event.
			if skillEvt := p.skillToolCallEvent(sess, msgIdx, msg, tc); skillEvt != nil {
				events = append(events, *skillEvt)
			}
		}

		// Skill loads via content tags (assistant messages with <skill_content name="xxx">).
		if msg.Role == session.RoleAssistant {
			events = append(events, p.skillContentTagEvents(sess, msgIdx, msg)...)
		}

		// Image usage from message images.
		events = append(events, p.imageEvents(sess, msgIdx, msg)...)
	}

	// Note: error events were already extracted above (step 2), before message scan.

	// Compute summary from extracted events.
	summary := NewSessionEventSummary(sess.ID, events)

	return events, summary
}

// ── Private extraction methods ──

// extractAgentEvent creates a single agent_detection event for the session.
func (p *Processor) extractAgentEvent(sess *session.Session) Event {
	return Event{
		ID:          uuid.New().String(),
		SessionID:   sess.ID,
		Type:        EventAgentDetection,
		OccurredAt:  sess.CreatedAt,
		ProjectPath: sess.ProjectPath,
		RemoteURL:   sess.RemoteURL,
		Provider:    sess.Provider,
		Agent:       sess.Agent,
		AgentInfo: &AgentDetail{
			Provider: sess.Provider,
			Agent:    sess.Agent,
		},
	}
}

// extractModelEvents creates agent_detection events for each unique model found.
func (p *Processor) extractModelEvents(sess *session.Session) []Event {
	modelsSeen := make(map[string]bool)
	var events []Event

	for msgIdx := range sess.Messages {
		msg := &sess.Messages[msgIdx]
		if msg.Model != "" && !modelsSeen[msg.Model] {
			modelsSeen[msg.Model] = true
			events = append(events, Event{
				ID:           uuid.New().String(),
				SessionID:    sess.ID,
				Type:         EventAgentDetection,
				MessageIndex: msgIdx,
				MessageID:    msg.ID,
				OccurredAt:   msg.Timestamp,
				ProjectPath:  sess.ProjectPath,
				RemoteURL:    sess.RemoteURL,
				Provider:     sess.Provider,
				Agent:        sess.Agent,
				AgentInfo: &AgentDetail{
					Provider: sess.Provider,
					Agent:    sess.Agent,
					Model:    msg.Model,
				},
			})
		}
	}

	return events
}

// toolCallEvent creates a tool_call event from a tool invocation.
func (p *Processor) toolCallEvent(sess *session.Session, msgIdx int, msg *session.Message, tc *session.ToolCall) Event {
	return Event{
		ID:           uuid.New().String(),
		SessionID:    sess.ID,
		Type:         EventToolCall,
		MessageIndex: msgIdx,
		MessageID:    msg.ID,
		OccurredAt:   msg.Timestamp,
		ProjectPath:  sess.ProjectPath,
		RemoteURL:    sess.RemoteURL,
		Provider:     sess.Provider,
		Agent:        sess.Agent,
		ToolCall: &ToolCallDetail{
			ToolName:     tc.Name,
			ToolCallID:   tc.ID,
			State:        tc.State,
			DurationMs:   tc.DurationMs,
			InputTokens:  tc.InputTokens,
			OutputTokens: tc.OutputTokens,
		},
	}
}

// commandEvent creates a command event if the tool call is a bash/shell command.
// Returns nil if the tool call is not a command.
func (p *Processor) commandEvent(sess *session.Session, msgIdx int, msg *session.Message, tc *session.ToolCall) *Event {
	baseCmd := session.ExtractBaseCommand(tc.Name, tc.Input)
	if baseCmd == "" {
		return nil
	}

	fullCmd := extractCommandString(tc.Input)
	if len(fullCmd) > maxFullCommandLen {
		fullCmd = fullCmd[:maxFullCommandLen]
	}

	return &Event{
		ID:           uuid.New().String(),
		SessionID:    sess.ID,
		Type:         EventCommand,
		MessageIndex: msgIdx,
		MessageID:    msg.ID,
		OccurredAt:   msg.Timestamp,
		ProjectPath:  sess.ProjectPath,
		RemoteURL:    sess.RemoteURL,
		Provider:     sess.Provider,
		Agent:        sess.Agent,
		Command: &CommandDetail{
			BaseCommand: baseCmd,
			FullCommand: fullCmd,
			ToolCallID:  tc.ID,
			State:       tc.State,
			DurationMs:  tc.DurationMs,
		},
	}
}

// skillToolCallEvent creates a skill_load event if the tool call loads a skill.
// Returns nil if this tool call doesn't load a skill.
func (p *Processor) skillToolCallEvent(sess *session.Session, msgIdx int, msg *session.Message, tc *session.ToolCall) *Event {
	if !isSkillToolName(tc.Name) {
		return nil
	}

	skillName := extractSkillName(tc.Input)
	if skillName == "" {
		skillName = extractSkillName(tc.Output)
	}
	if skillName == "" {
		return nil
	}

	return &Event{
		ID:           uuid.New().String(),
		SessionID:    sess.ID,
		Type:         EventSkillLoad,
		MessageIndex: msgIdx,
		MessageID:    msg.ID,
		OccurredAt:   msg.Timestamp,
		ProjectPath:  sess.ProjectPath,
		RemoteURL:    sess.RemoteURL,
		Provider:     sess.Provider,
		Agent:        sess.Agent,
		SkillLoad: &SkillLoadDetail{
			SkillName:  skillName,
			LoadMethod: "tool_call",
			ToolCallID: tc.ID,
		},
	}
}

// skillContentTagEvents extracts skill_load events from <skill_content> tags in assistant messages.
func (p *Processor) skillContentTagEvents(sess *session.Session, msgIdx int, msg *session.Message) []Event {
	names := extractSkillContentTags(msg.Content)
	if len(names) == 0 {
		return nil
	}

	events := make([]Event, 0, len(names))
	for _, name := range names {
		events = append(events, Event{
			ID:           uuid.New().String(),
			SessionID:    sess.ID,
			Type:         EventSkillLoad,
			MessageIndex: msgIdx,
			MessageID:    msg.ID,
			OccurredAt:   msg.Timestamp,
			ProjectPath:  sess.ProjectPath,
			RemoteURL:    sess.RemoteURL,
			Provider:     sess.Provider,
			Agent:        sess.Agent,
			SkillLoad: &SkillLoadDetail{
				SkillName:  name,
				LoadMethod: "content_tag",
			},
		})
	}

	return events
}

// imageEvents extracts image_usage events from message images.
func (p *Processor) imageEvents(sess *session.Session, msgIdx int, msg *session.Message) []Event {
	if len(msg.Images) == 0 {
		return nil
	}

	events := make([]Event, 0, len(msg.Images))
	for _, img := range msg.Images {
		events = append(events, Event{
			ID:           uuid.New().String(),
			SessionID:    sess.ID,
			Type:         EventImageUsage,
			MessageIndex: msgIdx,
			MessageID:    msg.ID,
			OccurredAt:   msg.Timestamp,
			ProjectPath:  sess.ProjectPath,
			RemoteURL:    sess.RemoteURL,
			Provider:     sess.Provider,
			Agent:        sess.Agent,
			Image: &ImageDetail{
				MediaType:      img.MediaType,
				SizeBytes:      img.SizeBytes,
				TokensEstimate: img.TokensEstimate,
				Source:         img.Source,
			},
		})
	}

	return events
}

// errorEvents creates error events from session.Errors.
func (p *Processor) errorEvents(sess *session.Session) []Event {
	if len(sess.Errors) == 0 {
		return nil
	}

	events := make([]Event, 0, len(sess.Errors))
	for _, err := range sess.Errors {
		events = append(events, Event{
			ID:           uuid.New().String(),
			SessionID:    sess.ID,
			Type:         EventError,
			MessageIndex: err.MessageIndex,
			MessageID:    err.MessageID,
			OccurredAt:   err.OccurredAt,
			ProjectPath:  sess.ProjectPath,
			RemoteURL:    sess.RemoteURL,
			Provider:     sess.Provider,
			Agent:        sess.Agent,
			Error: &ErrorDetail{
				Category:   err.Category,
				Source:     err.Source,
				Message:    err.Message,
				ToolName:   err.ToolName,
				HTTPStatus: err.HTTPStatus,
			},
		})
	}

	return events
}

// ── Helpers (local copies to avoid circular dependency with skillobs) ──

// isSkillToolName checks if a tool name indicates skill loading.
var skillToolNames = map[string]bool{
	"load_skill": true,
	"mcp_skill":  true,
	"skill":      true,
	"read_skill": true,
	"load-skill": true,
}

func isSkillToolName(name string) bool {
	return skillToolNames[strings.ToLower(name)]
}

// extractSkillName tries to find a skill name in a JSON string or plain text.
func extractSkillName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	// Not JSON — might be a plain skill name.
	if raw[0] != '{' && raw[0] != '"' {
		if !strings.ContainsAny(raw, " \t\n{}[]") {
			return raw
		}
		return ""
	}

	// Try as a plain quoted string.
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal([]byte(raw), &s); err == nil && s != "" {
			return s
		}
	}

	// Try as a JSON object with known fields.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return ""
	}

	for _, key := range []string{"name", "skill_name", "skill"} {
		if v, ok := obj[key]; ok {
			var s string
			if err := json.Unmarshal(v, &s); err == nil && s != "" {
				return s
			}
		}
	}

	return ""
}

// extractSkillContentTags finds <skill_content name="xxx"> patterns in text.
func extractSkillContentTags(text string) []string {
	var names []string
	const prefix = `<skill_content name="`

	remaining := text
	for {
		idx := strings.Index(remaining, prefix)
		if idx < 0 {
			break
		}
		remaining = remaining[idx+len(prefix):]
		end := strings.IndexByte(remaining, '"')
		if end < 0 {
			break
		}
		name := remaining[:end]
		if name != "" {
			names = append(names, name)
		}
		remaining = remaining[end+1:]
	}

	return names
}

// extractCommandString extracts the command string from tool input.
func extractCommandString(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}

	if strings.HasPrefix(input, "{") {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(input), &obj); err == nil {
			if cmd, ok := obj["command"].(string); ok {
				return strings.TrimSpace(cmd)
			}
			if cmd, ok := obj["cmd"].(string); ok {
				return strings.TrimSpace(cmd)
			}
			return ""
		}
	}

	return input
}
