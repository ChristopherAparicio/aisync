// Package converter transforms sessions between the unified aisync format
// and provider-native formats (Claude Code JSONL, OpenCode JSON).
package converter

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/domain"
)

// Converter implements domain.Converter for cross-provider format conversion.
type Converter struct{}

// New creates a new Converter.
func New() *Converter {
	return &Converter{}
}

// SupportedFormats returns which providers this converter supports.
func (c *Converter) SupportedFormats() []domain.ProviderName {
	return []domain.ProviderName{
		domain.ProviderClaudeCode,
		domain.ProviderOpenCode,
	}
}

// ToNative converts a unified Session to the native format of the target provider.
func (c *Converter) ToNative(session *domain.Session, target domain.ProviderName) ([]byte, error) {
	switch target {
	case domain.ProviderClaudeCode:
		return toClaudeJSONL(session)
	case domain.ProviderOpenCode:
		return toOpenCodeJSON(session)
	default:
		return nil, fmt.Errorf("unsupported target format %q", target)
	}
}

// FromNative parses raw provider-native data into a unified Session.
func (c *Converter) FromNative(data []byte, source domain.ProviderName) (*domain.Session, error) {
	switch source {
	case domain.ProviderClaudeCode:
		return fromClaudeJSONL(data)
	case domain.ProviderOpenCode:
		return fromOpenCodeJSON(data)
	default:
		return nil, fmt.Errorf("unsupported source format %q", source)
	}
}

// ToContextMD generates a CONTEXT.md fallback from a session.
// This is a universal output that any AI tool can read as a prompt.
func ToContextMD(session *domain.Session) []byte {
	var b strings.Builder

	b.WriteString("# AI Session Context\n\n")
	b.WriteString(fmt.Sprintf("- **Provider:** %s\n", session.Provider))
	b.WriteString(fmt.Sprintf("- **Agent:** %s\n", session.Agent))
	if session.Branch != "" {
		b.WriteString(fmt.Sprintf("- **Branch:** %s\n", session.Branch))
	}
	if !session.CreatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("- **Created:** %s\n", session.CreatedAt.Format(time.RFC3339)))
	}
	if session.Summary != "" {
		b.WriteString(fmt.Sprintf("- **Summary:** %s\n", session.Summary))
	}
	b.WriteString("\n---\n\n")

	// File changes
	if len(session.FileChanges) > 0 {
		b.WriteString("## Files Changed\n\n")
		for _, fc := range session.FileChanges {
			b.WriteString(fmt.Sprintf("- `%s` (%s)\n", fc.FilePath, fc.ChangeType))
		}
		b.WriteString("\n---\n\n")
	}

	// Conversation
	b.WriteString("## Conversation\n\n")
	for _, msg := range session.Messages {
		switch msg.Role {
		case domain.RoleUser:
			b.WriteString("### User\n\n")
		case domain.RoleAssistant:
			b.WriteString("### Assistant\n\n")
		case domain.RoleSystem:
			b.WriteString("### System\n\n")
		}

		if msg.Content != "" {
			b.WriteString(msg.Content)
			b.WriteString("\n\n")
		}

		// Tool calls
		for _, tc := range msg.ToolCalls {
			b.WriteString(fmt.Sprintf("**Tool: %s** (state: %s)\n", tc.Name, tc.State))
			if tc.Input != "" {
				b.WriteString("```json\n")
				b.WriteString(tc.Input)
				b.WriteString("\n```\n")
			}
			if tc.Output != "" {
				b.WriteString("\nOutput:\n```\n")
				b.WriteString(tc.Output)
				b.WriteString("\n```\n")
			}
			b.WriteString("\n")
		}
	}

	// Children
	for _, child := range session.Children {
		b.WriteString(fmt.Sprintf("\n---\n\n## Sub-agent: %s\n\n", child.Agent))
		for _, msg := range child.Messages {
			switch msg.Role {
			case domain.RoleUser:
				b.WriteString("### User\n\n")
			case domain.RoleAssistant:
				b.WriteString("### Assistant\n\n")
			default:
				b.WriteString("### System\n\n")
			}
			if msg.Content != "" {
				b.WriteString(msg.Content)
				b.WriteString("\n\n")
			}
		}
	}

	return []byte(b.String())
}

// DetectFormat tries to identify the format of raw data.
// Returns a ProviderName or empty string if it looks like unified aisync JSON.
func DetectFormat(data []byte) domain.ProviderName {
	trimmed := strings.TrimSpace(string(data))

	// JSONL: multiple lines of JSON (Claude Code)
	lines := strings.Split(trimmed, "\n")
	if len(lines) > 1 {
		// Check if first line is valid JSON object
		var first map[string]interface{}
		if err := json.Unmarshal([]byte(lines[0]), &first); err == nil {
			// Claude JSONL typically has "type" field
			if _, hasType := first["type"]; hasType {
				return domain.ProviderClaudeCode
			}
		}
	}

	// Single JSON object — could be aisync unified or OpenCode
	if strings.HasPrefix(trimmed, "{") {
		var obj map[string]interface{}
		if err := json.Unmarshal(data, &obj); err == nil {
			// aisync unified has "provider" and "messages" fields
			if _, hasProvider := obj["provider"]; hasProvider {
				if _, hasMsgs := obj["messages"]; hasMsgs {
					return "" // unified aisync format
				}
			}
			// OpenCode has "projectID" and "directory"
			if _, hasProjID := obj["projectID"]; hasProjID {
				return domain.ProviderOpenCode
			}
		}
	}

	return "" // default: assume unified aisync
}

// --- Claude Code JSONL conversion ---

// claudeJSONLLine is a single line in Claude Code JSONL.
type claudeJSONLLine struct {
	Cwd         string          `json:"cwd,omitempty"`
	UUID        string          `json:"uuid"`
	Timestamp   string          `json:"timestamp"`
	SessionID   string          `json:"sessionId"`
	GitBranch   string          `json:"gitBranch,omitempty"`
	Type        string          `json:"type"`
	Summary     string          `json:"summary,omitempty"`
	ParentUUID  string          `json:"parentUuid,omitempty"`
	Message     json.RawMessage `json:"message,omitempty"`
	IsSidechain bool            `json:"isSidechain"`
}

// claudeNativeMessage is the message envelope in Claude JSONL.
type claudeNativeMessage struct {
	Usage      *claudeUsage    `json:"usage,omitempty"`
	StopReason *string         `json:"stop_reason,omitempty"`
	Role       string          `json:"role"`
	Model      string          `json:"model,omitempty"`
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Content    json.RawMessage `json:"content"`
}

type claudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type claudeContentBlock struct {
	Content   interface{}     `json:"content,omitempty"`
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

func toClaudeJSONL(session *domain.Session) ([]byte, error) {
	var lines []string
	prevUUID := ""

	// Summary line
	if session.Summary != "" {
		summaryLine := claudeJSONLLine{
			Type:    "summary",
			Summary: session.Summary,
		}
		data, err := json.Marshal(summaryLine)
		if err != nil {
			return nil, err
		}
		lines = append(lines, string(data))
	}

	for i, msg := range session.Messages {
		uuid := msg.ID
		if uuid == "" {
			uuid = fmt.Sprintf("uuid-%03d", i+1)
		}

		ts := msg.Timestamp.Format(time.RFC3339Nano)

		// Determine parent
		parent := prevUUID

		// Build content blocks
		var contentBlocks []claudeContentBlock

		// For assistant messages: text + thinking + tool_use blocks
		if msg.Role == domain.RoleAssistant {
			if msg.Thinking != "" {
				contentBlocks = append(contentBlocks, claudeContentBlock{
					Type:     "thinking",
					Thinking: msg.Thinking,
				})
			}
			if msg.Content != "" {
				contentBlocks = append(contentBlocks, claudeContentBlock{
					Type: "text",
					Text: msg.Content,
				})
			}
			for _, tc := range msg.ToolCalls {
				inputRaw := json.RawMessage(tc.Input)
				if !json.Valid(inputRaw) {
					inputRaw = json.RawMessage(`{}`)
				}
				contentBlocks = append(contentBlocks, claudeContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: inputRaw,
				})
			}
		}

		// Build the native message
		var nativeContent json.RawMessage
		if msg.Role == domain.RoleUser && len(msg.ToolCalls) == 0 {
			// Simple user message: content is a plain string
			var err error
			nativeContent, err = json.Marshal(msg.Content)
			if err != nil {
				return nil, err
			}
		} else if msg.Role == domain.RoleUser && len(msg.ToolCalls) > 0 {
			// User message with tool_results
			var blocks []claudeContentBlock
			for _, tc := range msg.ToolCalls {
				blocks = append(blocks, claudeContentBlock{
					Type:      "tool_result",
					ToolUseID: tc.ID,
					Content:   tc.Output,
				})
			}
			var err error
			nativeContent, err = json.Marshal(blocks)
			if err != nil {
				return nil, err
			}
		} else {
			// Assistant with content blocks
			var err error
			nativeContent, err = json.Marshal(contentBlocks)
			if err != nil {
				return nil, err
			}
		}

		role := string(msg.Role)
		nativeMsg := claudeNativeMessage{
			Role:    role,
			Model:   msg.Model,
			ID:      msg.ID,
			Type:    "message",
			Content: nativeContent,
		}
		if msg.Tokens > 0 && msg.Role == domain.RoleAssistant {
			nativeMsg.Usage = &claudeUsage{
				InputTokens:  session.TokenUsage.InputTokens / max(len(session.Messages), 1),
				OutputTokens: msg.Tokens,
			}
		}

		msgData, err := json.Marshal(nativeMsg)
		if err != nil {
			return nil, err
		}

		line := claudeJSONLLine{
			Cwd:         session.ProjectPath,
			UUID:        uuid,
			Timestamp:   ts,
			SessionID:   string(session.ID),
			GitBranch:   session.Branch,
			Type:        role,
			ParentUUID:  parent,
			Message:     msgData,
			IsSidechain: false,
		}

		lineData, err := json.Marshal(line)
		if err != nil {
			return nil, err
		}
		lines = append(lines, string(lineData))

		prevUUID = uuid

		// After an assistant message with tool calls, emit tool_result user messages
		if msg.Role == domain.RoleAssistant && len(msg.ToolCalls) > 0 {
			var resultBlocks []claudeContentBlock
			for _, tc := range msg.ToolCalls {
				resultBlocks = append(resultBlocks, claudeContentBlock{
					Type:      "tool_result",
					ToolUseID: tc.ID,
					Content:   tc.Output,
					IsError:   tc.State == domain.ToolStateError,
				})
			}
			resultContent, resultErr := json.Marshal(resultBlocks)
			if resultErr != nil {
				return nil, resultErr
			}

			resultMsg := claudeNativeMessage{
				Role:    "user",
				Type:    "message",
				Content: resultContent,
			}
			resultMsgData, resultMarshalErr := json.Marshal(resultMsg)
			if resultMarshalErr != nil {
				return nil, resultMarshalErr
			}

			resultUUID := uuid + "-result"
			resultLine := claudeJSONLLine{
				Cwd:        session.ProjectPath,
				UUID:       resultUUID,
				Timestamp:  ts,
				SessionID:  string(session.ID),
				GitBranch:  session.Branch,
				Type:       "user",
				ParentUUID: uuid,
				Message:    resultMsgData,
			}
			resultLineData, resultLineErr := json.Marshal(resultLine)
			if resultLineErr != nil {
				return nil, resultLineErr
			}
			lines = append(lines, string(resultLineData))
			prevUUID = resultUUID
		}
	}

	// Flatten children into the linear stream
	for _, child := range session.Children {
		// Add a system-like marker for the sub-agent
		markerText := fmt.Sprintf("[Sub-agent: %s]", child.Agent)
		markerMsg := claudeNativeMessage{
			Role:    "assistant",
			Type:    "message",
			Content: json.RawMessage(`[{"type":"text","text":"` + markerText + `"}]`),
		}
		markerData, _ := json.Marshal(markerMsg)
		markerUUID := fmt.Sprintf("child-%s-marker", child.ID)
		markerLine := claudeJSONLLine{
			UUID:       markerUUID,
			Timestamp:  child.CreatedAt.Format(time.RFC3339Nano),
			SessionID:  string(session.ID),
			Type:       "assistant",
			ParentUUID: prevUUID,
			Message:    markerData,
		}
		markerLineData, _ := json.Marshal(markerLine)
		lines = append(lines, string(markerLineData))
		prevUUID = markerUUID
	}

	result := strings.Join(lines, "\n") + "\n"
	return []byte(result), nil
}

func fromClaudeJSONL(data []byte) (*domain.Session, error) {
	session := &domain.Session{
		Provider:    domain.ProviderClaudeCode,
		Agent:       "claude",
		StorageMode: domain.StorageModeFull,
		Version:     1,
		ExportedBy:  "aisync-import",
		ExportedAt:  time.Now(),
	}

	toolUses := make(map[string]*domain.ToolCall)
	fileChanges := make(map[string]domain.ChangeType)
	var totalInput, totalOutput int

	lineStrs := strings.Split(strings.TrimSpace(string(data)), "\n")
	for _, lineStr := range lineStrs {
		if lineStr == "" {
			continue
		}
		var line claudeJSONLLine
		if err := json.Unmarshal([]byte(lineStr), &line); err != nil {
			continue
		}

		if session.Branch == "" && line.GitBranch != "" {
			session.Branch = line.GitBranch
		}
		if session.ProjectPath == "" && line.Cwd != "" {
			session.ProjectPath = line.Cwd
		}
		if session.ID == "" && line.SessionID != "" {
			session.ID = domain.SessionID(line.SessionID)
		}

		switch line.Type {
		case "summary":
			session.Summary = line.Summary
		case "user", "assistant":
			if line.IsSidechain || len(line.Message) == 0 {
				continue
			}
			var msg claudeNativeMessage
			if err := json.Unmarshal(line.Message, &msg); err != nil {
				continue
			}

			ts, _ := time.Parse(time.RFC3339Nano, line.Timestamp)

			if msg.Usage != nil {
				totalInput += msg.Usage.InputTokens
				totalOutput += msg.Usage.OutputTokens
			}

			domainMsg := domain.Message{
				ID:        line.UUID,
				Timestamp: ts,
				Model:     msg.Model,
			}
			if msg.Role == "user" {
				domainMsg.Role = domain.RoleUser
			} else {
				domainMsg.Role = domain.RoleAssistant
			}

			// Parse content
			parsed := parseClaudeContent(msg.Content, msg.Role, toolUses, fileChanges)
			domainMsg.Content = parsed.text
			domainMsg.Thinking = parsed.thinking
			domainMsg.ToolCalls = parsed.toolCalls
			if msg.Usage != nil {
				domainMsg.Tokens = msg.Usage.InputTokens + msg.Usage.OutputTokens
			}

			// Match tool results
			if msg.Role == "user" && len(parsed.toolResults) > 0 {
				for _, tr := range parsed.toolResults {
					if tc, ok := toolUses[tr.toolUseID]; ok {
						tc.Output = tr.output
						if tr.isError {
							tc.State = domain.ToolStateError
						} else {
							tc.State = domain.ToolStateCompleted
						}
					}
				}
				if parsed.text == "" {
					continue
				}
			}

			session.Messages = append(session.Messages, domainMsg)
			if session.CreatedAt.IsZero() {
				session.CreatedAt = ts
			}
		}
	}

	session.TokenUsage = domain.TokenUsage{
		InputTokens:  totalInput,
		OutputTokens: totalOutput,
		TotalTokens:  totalInput + totalOutput,
	}

	for path, ct := range fileChanges {
		session.FileChanges = append(session.FileChanges, domain.FileChange{
			FilePath:   path,
			ChangeType: ct,
		})
	}

	if session.ID == "" {
		session.ID = domain.NewSessionID()
	}

	return session, nil
}

type parsedClaudeContent struct {
	text        string
	thinking    string
	toolCalls   []domain.ToolCall
	toolResults []claudeToolResult
}

type claudeToolResult struct {
	toolUseID string
	output    string
	isError   bool
}

func parseClaudeContent(raw json.RawMessage, role string, toolUses map[string]*domain.ToolCall, fileChanges map[string]domain.ChangeType) parsedClaudeContent {
	result := parsedClaudeContent{}

	// Try as string
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		result.text = s
		return result
	}

	// Try as array of content blocks
	var blocks []claudeContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return result
	}

	var textParts []string
	for _, block := range blocks {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "thinking":
			result.thinking = block.Thinking
		case "tool_use":
			tc := domain.ToolCall{
				ID:    block.ID,
				Name:  block.Name,
				Input: string(block.Input),
				State: domain.ToolStatePending,
			}
			result.toolCalls = append(result.toolCalls, tc)
			toolUses[block.ID] = &result.toolCalls[len(result.toolCalls)-1]
			trackClaudeFileChange(block.Name, block.Input, fileChanges)
		case "tool_result":
			output := ""
			if block.Content != nil {
				switch v := block.Content.(type) {
				case string:
					output = v
				default:
					outputBytes, _ := json.Marshal(v)
					output = string(outputBytes)
				}
			}
			result.toolResults = append(result.toolResults, claudeToolResult{
				toolUseID: block.ToolUseID,
				output:    output,
				isError:   block.IsError,
			})
		}
	}

	_ = role
	result.text = strings.Join(textParts, "\n")
	return result
}

func trackClaudeFileChange(toolName string, rawInput json.RawMessage, changes map[string]domain.ChangeType) {
	var input struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(rawInput, &input); err != nil || input.FilePath == "" {
		return
	}
	switch toolName {
	case "Write":
		if _, exists := changes[input.FilePath]; !exists {
			changes[input.FilePath] = domain.ChangeCreated
		} else {
			changes[input.FilePath] = domain.ChangeModified
		}
	case "Edit":
		changes[input.FilePath] = domain.ChangeModified
	case "Read":
		if _, exists := changes[input.FilePath]; !exists {
			changes[input.FilePath] = domain.ChangeRead
		}
	}
}

// --- OpenCode JSON conversion ---

// openCodeExport is the JSON structure for OpenCode export format.
// It bundles session + messages + parts into a single JSON object.
type openCodeExport struct {
	Messages []ocExportMsg   `json:"messages"`
	Session  ocExportSession `json:"session"`
}

type ocExportSession struct {
	ID        string      `json:"id"`
	ProjectID string      `json:"projectID"`
	Directory string      `json:"directory"`
	ParentID  string      `json:"parentID,omitempty"`
	Title     string      `json:"title"`
	Time      ocTimeStamp `json:"time"`
}

type ocTimeStamp struct {
	Created int64 `json:"created"`
	Updated int64 `json:"updated"`
}

type ocExportMsg struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Agent   string          `json:"agent,omitempty"`
	ModelID string          `json:"modelID,omitempty"`
	Content string          `json:"content,omitempty"`
	Parts   []ocExportPart  `json:"parts"`
	Tokens  ocExportTokens  `json:"tokens"`
	Time    ocExportMsgTime `json:"time"`
}

type ocExportMsgTime struct {
	Created   int64 `json:"created"`
	Completed int64 `json:"completed"`
}

type ocExportPart struct {
	State *ocExportState `json:"state,omitempty"`
	ID    string         `json:"id"`
	Type  string         `json:"type"`
	Text  string         `json:"text,omitempty"`
	Tool  string         `json:"tool,omitempty"`
}

type ocExportState struct {
	Input  interface{} `json:"input,omitempty"`
	Output string      `json:"output,omitempty"`
	Status string      `json:"status"`
}

type ocExportTokens struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

func toOpenCodeJSON(session *domain.Session) ([]byte, error) {
	export := openCodeExport{
		Session: ocExportSession{
			ID:        string(session.ID),
			Directory: session.ProjectPath,
			Title:     session.Summary,
			Time: ocTimeStamp{
				Created: session.CreatedAt.UnixMilli(),
				Updated: session.ExportedAt.UnixMilli(),
			},
		},
	}

	for _, msg := range session.Messages {
		ocMsg := ocExportMsg{
			ID:      msg.ID,
			Role:    string(msg.Role),
			Agent:   session.Agent,
			ModelID: msg.Model,
			Content: msg.Content,
			Tokens: ocExportTokens{
				Input:  msg.Tokens / 2, // rough split
				Output: msg.Tokens / 2,
			},
			Time: ocExportMsgTime{
				Created:   msg.Timestamp.UnixMilli(),
				Completed: msg.Timestamp.UnixMilli(),
			},
		}

		// Text part
		if msg.Content != "" {
			ocMsg.Parts = append(ocMsg.Parts, ocExportPart{
				ID:   msg.ID + "-text",
				Type: "text",
				Text: msg.Content,
			})
		}

		// Thinking part
		if msg.Thinking != "" {
			ocMsg.Parts = append(ocMsg.Parts, ocExportPart{
				ID:   msg.ID + "-reasoning",
				Type: "reasoning",
				Text: msg.Thinking,
			})
		}

		// Tool call parts
		for _, tc := range msg.ToolCalls {
			var input interface{}
			if tc.Input != "" && json.Valid([]byte(tc.Input)) {
				_ = json.Unmarshal([]byte(tc.Input), &input)
			}
			status := string(tc.State)
			ocMsg.Parts = append(ocMsg.Parts, ocExportPart{
				ID:   tc.ID,
				Type: "tool",
				Tool: tc.Name,
				State: &ocExportState{
					Input:  input,
					Output: tc.Output,
					Status: status,
				},
			})
		}

		export.Messages = append(export.Messages, ocMsg)
	}

	return json.MarshalIndent(export, "", "  ")
}

func fromOpenCodeJSON(data []byte) (*domain.Session, error) {
	var export openCodeExport
	if err := json.Unmarshal(data, &export); err != nil {
		return nil, fmt.Errorf("parsing OpenCode JSON: %w", err)
	}

	session := &domain.Session{
		ID:          domain.SessionID(export.Session.ID),
		Provider:    domain.ProviderOpenCode,
		Agent:       "coder",
		StorageMode: domain.StorageModeFull,
		Summary:     export.Session.Title,
		ProjectPath: export.Session.Directory,
		Version:     1,
		ExportedBy:  "aisync-import",
		ExportedAt:  time.Now(),
		CreatedAt:   time.UnixMilli(export.Session.Time.Created),
	}

	if export.Session.ParentID != "" {
		session.ParentID = domain.SessionID(export.Session.ParentID)
	}

	var totalInput, totalOutput int
	for _, msg := range export.Messages {
		if msg.Agent != "" {
			session.Agent = msg.Agent
		}

		domainMsg := domain.Message{
			ID:        msg.ID,
			Model:     msg.ModelID,
			Timestamp: time.UnixMilli(msg.Time.Created),
		}

		switch msg.Role {
		case "user":
			domainMsg.Role = domain.RoleUser
		case "assistant":
			domainMsg.Role = domain.RoleAssistant
		default:
			domainMsg.Role = domain.RoleSystem
		}

		totalInput += msg.Tokens.Input
		totalOutput += msg.Tokens.Output
		domainMsg.Tokens = msg.Tokens.Input + msg.Tokens.Output

		// Process parts
		var textParts []string
		for _, part := range msg.Parts {
			switch part.Type {
			case "text":
				textParts = append(textParts, part.Text)
			case "reasoning":
				domainMsg.Thinking = part.Text
			case "tool":
				tc := domain.ToolCall{
					ID:   part.ID,
					Name: part.Tool,
				}
				if part.State != nil {
					tc.Output = part.State.Output
					switch part.State.Status {
					case "completed":
						tc.State = domain.ToolStateCompleted
					case "error":
						tc.State = domain.ToolStateError
					case "running":
						tc.State = domain.ToolStateRunning
					default:
						tc.State = domain.ToolStatePending
					}
					if part.State.Input != nil {
						inputBytes, _ := json.Marshal(part.State.Input)
						tc.Input = string(inputBytes)
					}
				}
				domainMsg.ToolCalls = append(domainMsg.ToolCalls, tc)
			}
		}

		if msg.Content != "" && len(textParts) == 0 {
			domainMsg.Content = msg.Content
		} else {
			domainMsg.Content = strings.Join(textParts, "\n")
		}

		session.Messages = append(session.Messages, domainMsg)
	}

	session.TokenUsage = domain.TokenUsage{
		InputTokens:  totalInput,
		OutputTokens: totalOutput,
		TotalTokens:  totalInput + totalOutput,
	}

	if session.ID == "" {
		session.ID = domain.NewSessionID()
	}

	return session, nil
}
