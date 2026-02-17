// Package claude implements the Claude Code provider for aisync.
// It reads and writes sessions from Claude Code's JSONL-based session storage
// located in ~/.claude/projects/{encoded-path}/{session-uuid}.jsonl.
package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/domain"
)

const (
	claudeDir       = ".claude"
	projectsDir     = "projects"
	sessionsIndex   = "sessions-index.json"
	defaultAgent    = "claude"
	jsonlExtension  = ".jsonl"
	exportedByLabel = "aisync"
)

// Provider implements domain.Provider for Claude Code.
type Provider struct {
	// claudeHome overrides the default ~/.claude path (for testing).
	claudeHome string
}

// New creates a Claude Code provider.
// If claudeHome is empty, it defaults to ~/.claude.
func New(claudeHome string) *Provider {
	if claudeHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			claudeHome = filepath.Join("~", claudeDir)
		} else {
			claudeHome = filepath.Join(home, claudeDir)
		}
	}
	return &Provider{claudeHome: claudeHome}
}

// Name returns the provider identifier.
func (p *Provider) Name() domain.ProviderName {
	return domain.ProviderClaudeCode
}

// Detect finds sessions matching the given project and branch.
func (p *Provider) Detect(projectPath string, branch string) ([]domain.SessionSummary, error) {
	projectDir := p.projectDir(projectPath)
	indexPath := filepath.Join(projectDir, sessionsIndex)

	index, err := readSessionsIndex(indexPath)
	if err != nil {
		return nil, fmt.Errorf("reading sessions index: %w", err)
	}

	var matches []domain.SessionSummary
	for _, entry := range index.Entries {
		// Filter by branch if specified
		if branch != "" && entry.GitBranch != branch {
			continue
		}
		// Filter by project path
		if entry.ProjectPath != "" && entry.ProjectPath != projectPath {
			continue
		}

		created, _ := time.Parse(time.RFC3339, entry.Created)
		matches = append(matches, domain.SessionSummary{
			ID:           domain.SessionID(entry.SessionID),
			Provider:     domain.ProviderClaudeCode,
			Agent:        defaultAgent,
			Branch:       entry.GitBranch,
			Summary:      entry.Summary,
			MessageCount: entry.MessageCount,
			CreatedAt:    created,
		})
	}

	// Sort by created_at descending (most recent first)
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].CreatedAt.After(matches[j].CreatedAt)
	})

	return matches, nil
}

// Export reads a session JSONL file and converts it to the unified Session model.
func (p *Provider) Export(sessionID domain.SessionID, mode domain.StorageMode) (*domain.Session, error) {
	// Find the JSONL file for this session
	jsonlPath, err := p.findSessionFile(sessionID)
	if err != nil {
		return nil, err
	}

	lines, err := readJSONLFile(jsonlPath)
	if err != nil {
		return nil, fmt.Errorf("reading session file: %w", err)
	}

	return parseSession(sessionID, lines, mode)
}

// CanImport reports that Claude Code supports session import.
func (p *Provider) CanImport() bool {
	return true
}

// Import writes a session back to Claude Code's native JSONL format.
// For MVP, this copies the JSONL file to the appropriate project directory.
func (p *Provider) Import(session *domain.Session) error {
	// TODO: Implement in Milestone 1.6 (Export/Import)
	return domain.ErrImportNotSupported
}

// projectDir returns the Claude Code project directory for a given project path.
// Claude Code encodes paths by replacing "/" with "-".
func (p *Provider) projectDir(projectPath string) string {
	encoded := encodeProjectPath(projectPath)
	return filepath.Join(p.claudeHome, projectsDir, encoded)
}

// findSessionFile locates the JSONL file for a given session ID.
// It searches all project directories.
func (p *Provider) findSessionFile(sessionID domain.SessionID) (string, error) {
	projectsRoot := filepath.Join(p.claudeHome, projectsDir)
	entries, err := os.ReadDir(projectsRoot)
	if err != nil {
		return "", fmt.Errorf("reading projects directory: %w", err)
	}

	fileName := string(sessionID) + jsonlExtension
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(projectsRoot, entry.Name(), fileName)
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, nil
		}
	}

	return "", domain.ErrSessionNotFound
}

// encodeProjectPath converts an absolute path to Claude Code's directory naming.
// Each "/" is replaced with "-".
func encodeProjectPath(projectPath string) string {
	return strings.ReplaceAll(projectPath, "/", "-")
}

// --- JSON structures for Claude Code's native format ---

type sessionsIndexFile struct {
	Entries []indexEntry `json:"entries"`
	Version int          `json:"version"`
}

type indexEntry struct {
	SessionID    string `json:"sessionId"`
	FullPath     string `json:"fullPath"`
	FirstPrompt  string `json:"firstPrompt"`
	Created      string `json:"created"`
	Modified     string `json:"modified"`
	GitBranch    string `json:"gitBranch"`
	ProjectPath  string `json:"projectPath"`
	Summary      string `json:"summary"`
	LeafUUID     string `json:"leafUuid"`
	FileMtime    int64  `json:"fileMtime"`
	MessageCount int    `json:"messageCount"`
	IsSidechain  bool   `json:"isSidechain"`
}

// jsonlLine is the raw envelope for a single line in the JSONL file.
type jsonlLine struct {
	Cwd                     string          `json:"cwd"`
	SourceToolAssistantUUID string          `json:"sourceToolAssistantUUID"`
	UUID                    string          `json:"uuid"`
	Timestamp               string          `json:"timestamp"`
	SessionID               string          `json:"sessionId"`
	GitBranch               string          `json:"gitBranch"`
	Content                 string          `json:"content"`
	Subtype                 string          `json:"subtype"`
	Type                    string          `json:"type"`
	Summary                 string          `json:"summary"`
	LeafUUID                string          `json:"leafUuid"`
	ParentUUID              string          `json:"parentUuid"`
	Message                 json.RawMessage `json:"message"`
	IsSidechain             bool            `json:"isSidechain"`
}

// claudeMessage is the inner message object.
type claudeMessage struct {
	Usage      *claudeUsage    `json:"usage"`
	StopReason *string         `json:"stop_reason"`
	Role       string          `json:"role"`
	Model      string          `json:"model"`
	ID         string          `json:"id"`
	Content    json.RawMessage `json:"content"`
}

type claudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// Content block types
type contentBlock struct {
	Type string `json:"type"`

	// text block
	Text string `json:"text"`

	// thinking block
	Thinking string `json:"thinking"`

	// tool_use block
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`

	// tool_result block
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"` // string or array
	IsError   bool            `json:"is_error"`
}

// --- Parsing logic ---

func readSessionsIndex(path string) (*sessionsIndexFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var index sessionsIndexFile
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("parsing sessions index: %w", err)
	}

	return &index, nil
}

func readJSONLFile(path string) ([]jsonlLine, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var lines []jsonlLine
	scanner := bufio.NewScanner(f)
	// Increase buffer size for large lines (tool results can be huge)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		var line jsonlLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			// Skip malformed lines
			continue
		}
		lines = append(lines, line)
	}

	return lines, scanner.Err()
}

func parseSession(sessionID domain.SessionID, lines []jsonlLine, mode domain.StorageMode) (*domain.Session, error) {
	session := &domain.Session{
		ID:          sessionID,
		Provider:    domain.ProviderClaudeCode,
		Agent:       defaultAgent,
		StorageMode: mode,
		Version:     1,
		ExportedBy:  exportedByLabel,
		ExportedAt:  time.Now(),
	}

	// Collect tool_use blocks to match with tool_results later
	toolUses := make(map[string]*domain.ToolCall) // keyed by tool_use id

	// Track file changes from Write/Edit tool uses
	fileChanges := make(map[string]domain.ChangeType)

	var (
		totalInput  int
		totalOutput int
	)

	for _, line := range lines {
		// Extract metadata from first message
		if session.Branch == "" && line.GitBranch != "" {
			session.Branch = line.GitBranch
		}
		if session.ProjectPath == "" && line.Cwd != "" {
			session.ProjectPath = line.Cwd
		}

		switch line.Type {
		case "summary":
			session.Summary = line.Summary

		case "user", "assistant":
			if line.IsSidechain {
				continue // Skip subagent messages for now
			}
			if len(line.Message) == 0 {
				continue
			}

			var msg claudeMessage
			if err := json.Unmarshal(line.Message, &msg); err != nil {
				continue
			}

			ts, _ := time.Parse(time.RFC3339Nano, line.Timestamp)

			// Track tokens
			if msg.Usage != nil {
				totalInput += msg.Usage.InputTokens + msg.Usage.CacheCreationInputTokens + msg.Usage.CacheReadInputTokens
				totalOutput += msg.Usage.OutputTokens
			}

			// summary mode: only metadata, no messages
			if mode == domain.StorageModeSummary {
				// Still set CreatedAt from first message
				if session.CreatedAt.IsZero() {
					session.CreatedAt = ts
				}
				continue
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

			// Parse content blocks
			parsed := parseContentBlocks(msg.Content, msg.Role, mode, toolUses, fileChanges)
			domainMsg.Content = parsed.text
			domainMsg.Thinking = parsed.thinking
			domainMsg.ToolCalls = parsed.toolCalls
			domainMsg.Tokens = parsed.tokens
			if msg.Usage != nil {
				domainMsg.Tokens = msg.Usage.InputTokens + msg.Usage.OutputTokens
			}

			// For tool_result user messages, match with pending tool_uses
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
				// Don't add tool_result-only user messages as separate messages
				// (they're merged into the tool call on the assistant message)
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

	// Set token usage
	session.TokenUsage = domain.TokenUsage{
		InputTokens:  totalInput,
		OutputTokens: totalOutput,
		TotalTokens:  totalInput + totalOutput,
	}

	// Set file changes
	for path, changeType := range fileChanges {
		session.FileChanges = append(session.FileChanges, domain.FileChange{
			FilePath:   path,
			ChangeType: changeType,
		})
	}

	return session, nil
}

type parsedContent struct {
	text        string
	thinking    string
	toolCalls   []domain.ToolCall
	toolResults []toolResultInfo
	tokens      int
}

type toolResultInfo struct {
	toolUseID string
	output    string
	isError   bool
}

func parseContentBlocks(raw json.RawMessage, role string, mode domain.StorageMode, toolUses map[string]*domain.ToolCall, fileChanges map[string]domain.ChangeType) parsedContent {
	result := parsedContent{}

	// Content can be a string (simple user message) or an array of blocks
	var contentStr string
	if err := json.Unmarshal(raw, &contentStr); err == nil {
		result.text = contentStr
		return result
	}

	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return result
	}

	var textParts []string

	for _, block := range blocks {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)

		case "thinking":
			if mode == domain.StorageModeFull {
				result.thinking = block.Thinking
			}

		case "tool_use":
			if mode == domain.StorageModeFull || mode == domain.StorageModeCompact {
				inputStr := string(block.Input)
				tc := domain.ToolCall{
					ID:    block.ID,
					Name:  block.Name,
					Input: inputStr,
					State: domain.ToolStatePending, // Will be updated when tool_result arrives
				}
				result.toolCalls = append(result.toolCalls, tc)
				toolUses[block.ID] = &result.toolCalls[len(result.toolCalls)-1]

				// Track file changes from Write/Edit tools
				trackFileChange(block.Name, block.Input, fileChanges)
			}

		case "tool_result":
			output := extractToolResultContent(block.Content)
			result.toolResults = append(result.toolResults, toolResultInfo{
				toolUseID: block.ToolUseID,
				output:    output,
				isError:   block.IsError,
			})
		}
	}

	result.text = strings.Join(textParts, "\n")
	return result
}

// extractToolResultContent gets the text from a tool_result content field.
// Content can be a string or an array of {type:"text", text:"..."} blocks.
func extractToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Try string first
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	// Try array of text blocks
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}

	return string(raw)
}

// trackFileChange infers file changes from tool calls.
func trackFileChange(toolName string, rawInput json.RawMessage, changes map[string]domain.ChangeType) {
	var input struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(rawInput, &input); err != nil || input.FilePath == "" {
		return
	}

	switch toolName {
	case "Write":
		// Write could be create or modify, default to modified
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
