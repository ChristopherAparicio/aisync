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

	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/google/uuid"
)

const (
	claudeDir       = ".claude"
	projectsDir     = "projects"
	sessionsIndex   = "sessions-index.json"
	defaultAgent    = "claude"
	jsonlExtension  = ".jsonl"
	exportedByLabel = "aisync"
)

// Provider implements session.Provider for Claude Code.
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
func (p *Provider) Name() session.ProviderName {
	return session.ProviderClaudeCode
}

// Detect finds sessions matching the given project and branch.
func (p *Provider) Detect(projectPath string, branch string) ([]session.Summary, error) {
	projectDir := p.projectDir(projectPath)
	indexPath := filepath.Join(projectDir, sessionsIndex)

	index, err := readSessionsIndex(indexPath)
	if err != nil {
		return nil, fmt.Errorf("reading sessions index: %w", err)
	}

	var matches []session.Summary
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
		matches = append(matches, session.Summary{
			ID:           session.ID(entry.SessionID),
			Provider:     session.ProviderClaudeCode,
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

// SessionFreshness returns the message count and last-updated timestamp
// for a Claude Code session, enabling the skip-if-unchanged optimization.
// Uses the JSONL file's modification time and counts message lines.
// Implements provider.FreshnessChecker.
func (p *Provider) SessionFreshness(sessionID session.ID) (*provider.Freshness, error) {
	jsonlPath, err := p.findSessionFile(sessionID)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(jsonlPath)
	if err != nil {
		return nil, fmt.Errorf("stat session file: %w", err)
	}

	// Count message lines (user/assistant) — skip summary/metadata lines.
	msgCount, err := countJSONLMessages(jsonlPath)
	if err != nil {
		return nil, fmt.Errorf("counting messages: %w", err)
	}

	return &provider.Freshness{
		MessageCount: msgCount,
		UpdatedAt:    info.ModTime().UnixMilli(),
	}, nil
}

// countJSONLMessages counts lines in a JSONL file that represent actual
// messages (type "user" or "assistant"). This is a fast scan that only
// unmarshals the "type" field, not the full message content.
func countJSONLMessages(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var count int
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		// Fast path: only unmarshal the type field.
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &header); err != nil {
			continue
		}
		if header.Type == "user" || header.Type == "assistant" {
			count++
		}
	}

	return count, scanner.Err()
}

// Export reads a session JSONL file and converts it to the unified Session model.
func (p *Provider) Export(sessionID session.ID, mode session.StorageMode) (*session.Session, error) {
	// Find the JSONL file for this session
	jsonlPath, err := p.findSessionFile(sessionID)
	if err != nil {
		return nil, err
	}

	lines, err := readJSONLFile(jsonlPath)
	if err != nil {
		return nil, fmt.Errorf("reading session file: %w", err)
	}

	sess, err := parseSession(sessionID, lines, mode)
	if err != nil {
		return nil, err
	}

	// Set SourceUpdatedAt from JSONL file modification time so that the
	// skip-if-unchanged optimization can compare stored vs source timestamps.
	if info, statErr := os.Stat(jsonlPath); statErr == nil {
		sess.SourceUpdatedAt = info.ModTime().UnixMilli()
	}

	return sess, nil
}

// CanImport reports that Claude Code supports session import.
func (p *Provider) CanImport() bool {
	return true
}

// Import writes a session back to Claude Code's native JSONL format.
// It converts the unified session to JSONL, writes it to the project directory,
// and updates the sessions-index.json file.
func (p *Provider) Import(sess *session.Session) error {
	if sess == nil {
		return fmt.Errorf("session is nil")
	}

	// Determine the project directory
	projectPath := sess.ProjectPath
	if projectPath == "" {
		return fmt.Errorf("session has no project path")
	}
	projDir := p.projectDir(projectPath)

	// Ensure directory exists
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		return fmt.Errorf("creating project directory: %w", err)
	}

	// Generate session ID if empty
	sessionID := string(sess.ID)
	if sessionID == "" {
		sessionID = uuid.New().String()
		sess.ID = session.ID(sessionID)
	}

	// Build JSONL content
	jsonlData, err := MarshalJSONL(sess)
	if err != nil {
		return fmt.Errorf("building JSONL: %w", err)
	}

	// Write JSONL file
	jsonlPath := filepath.Join(projDir, sessionID+jsonlExtension)
	if err := os.WriteFile(jsonlPath, jsonlData, 0o644); err != nil {
		return fmt.Errorf("writing JSONL file: %w", err)
	}

	// Update sessions-index.json
	if err := p.updateSessionsIndex(sess, projDir); err != nil {
		return fmt.Errorf("updating sessions index: %w", err)
	}

	return nil
}

// MarshalJSONL converts a unified Session to Claude Code JSONL bytes.
// This is a pure function with no I/O — it only serializes data.
// It is the canonical marshaling logic for Claude's native format.
func MarshalJSONL(sess *session.Session) ([]byte, error) {
	var lines []string
	prevUUID := ""

	// Summary line
	if sess.Summary != "" {
		summaryLine := jsonlLine{
			Type:      "summary",
			Summary:   sess.Summary,
			SessionID: string(sess.ID),
		}
		data, err := json.Marshal(summaryLine)
		if err != nil {
			return nil, err
		}
		lines = append(lines, string(data))
	}

	for _, msg := range sess.Messages {
		msgUUID := msg.ID
		if msgUUID == "" {
			msgUUID = uuid.New().String()
		}

		ts := msg.Timestamp.Format(time.RFC3339Nano)
		if msg.Timestamp.IsZero() {
			ts = time.Now().Format(time.RFC3339Nano)
		}

		// Build content blocks
		var nativeContent json.RawMessage

		if msg.Role == session.RoleUser && len(msg.ToolCalls) == 0 {
			// Simple user message: content is a plain string
			var err error
			nativeContent, err = json.Marshal(msg.Content)
			if err != nil {
				return nil, err
			}
		} else if msg.Role == session.RoleUser && len(msg.ToolCalls) > 0 {
			// User message with tool_results
			var blocks []contentBlock
			for _, tc := range msg.ToolCalls {
				resultContent, _ := json.Marshal(tc.Output)
				blocks = append(blocks, contentBlock{
					Type:      "tool_result",
					ToolUseID: tc.ID,
					Content:   resultContent,
					IsError:   tc.State == session.ToolStateError,
				})
			}
			var err error
			nativeContent, err = json.Marshal(blocks)
			if err != nil {
				return nil, err
			}
		} else {
			// Assistant message with content blocks
			var blocks []contentBlock

			if msg.Thinking != "" {
				blocks = append(blocks, contentBlock{
					Type:     "thinking",
					Thinking: msg.Thinking,
				})
			}
			if msg.Content != "" {
				blocks = append(blocks, contentBlock{
					Type: "text",
					Text: msg.Content,
				})
			}
			for _, tc := range msg.ToolCalls {
				inputRaw := json.RawMessage(tc.Input)
				if !json.Valid(inputRaw) {
					inputRaw = json.RawMessage(`{}`)
				}
				blocks = append(blocks, contentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: inputRaw,
				})
			}

			var err error
			nativeContent, err = json.Marshal(blocks)
			if err != nil {
				return nil, err
			}
		}

		role := string(msg.Role)
		nativeMsg := claudeMessage{
			Role:    role,
			Model:   msg.Model,
			ID:      msg.ID,
			Content: nativeContent,
		}
		if (msg.InputTokens > 0 || msg.OutputTokens > 0) && msg.Role == session.RoleAssistant {
			nativeMsg.Usage = &claudeUsage{
				InputTokens:  msg.InputTokens,
				OutputTokens: msg.OutputTokens,
			}
		}

		msgData, err := json.Marshal(nativeMsg)
		if err != nil {
			return nil, err
		}

		line := jsonlLine{
			Cwd:        sess.ProjectPath,
			UUID:       msgUUID,
			Timestamp:  ts,
			SessionID:  string(sess.ID),
			GitBranch:  sess.Branch,
			Type:       role,
			ParentUUID: prevUUID,
			Message:    msgData,
		}

		lineData, err := json.Marshal(line)
		if err != nil {
			return nil, err
		}
		lines = append(lines, string(lineData))
		prevUUID = msgUUID

		// After an assistant message with tool calls, emit tool_result user messages
		if msg.Role == session.RoleAssistant && len(msg.ToolCalls) > 0 {
			var resultBlocks []contentBlock
			for _, tc := range msg.ToolCalls {
				resultContent, _ := json.Marshal(tc.Output)
				resultBlocks = append(resultBlocks, contentBlock{
					Type:      "tool_result",
					ToolUseID: tc.ID,
					Content:   resultContent,
					IsError:   tc.State == session.ToolStateError,
				})
			}
			resultContent, resultErr := json.Marshal(resultBlocks)
			if resultErr != nil {
				return nil, resultErr
			}

			resultMsg := claudeMessage{
				Role:    "user",
				Content: resultContent,
			}
			resultMsgData, resultMarshalErr := json.Marshal(resultMsg)
			if resultMarshalErr != nil {
				return nil, resultMarshalErr
			}

			resultUUID := msgUUID + "-result"
			resultLine := jsonlLine{
				Cwd:        sess.ProjectPath,
				UUID:       resultUUID,
				Timestamp:  ts,
				SessionID:  string(sess.ID),
				GitBranch:  sess.Branch,
				Type:       "user",
				ParentUUID: msgUUID,
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

	result := strings.Join(lines, "\n") + "\n"
	return []byte(result), nil
}

// updateSessionsIndex adds or updates a session entry in the sessions-index.json.
func (p *Provider) updateSessionsIndex(sess *session.Session, projDir string) error {
	indexPath := filepath.Join(projDir, sessionsIndex)

	// Read existing index or create new
	var index sessionsIndexFile
	data, err := os.ReadFile(indexPath)
	if err == nil {
		if parseErr := json.Unmarshal(data, &index); parseErr != nil {
			// Corrupted index — start fresh
			index = sessionsIndexFile{Version: 1}
		}
	} else {
		index = sessionsIndexFile{Version: 1}
	}

	now := time.Now().Format(time.RFC3339)
	sessionID := string(sess.ID)

	// Find the last UUID for leafUuid
	leafUUID := ""
	if len(sess.Messages) > 0 {
		lastMsg := sess.Messages[len(sess.Messages)-1]
		leafUUID = lastMsg.ID
	}

	// Check if entry already exists
	found := false
	for i, entry := range index.Entries {
		if entry.SessionID == sessionID {
			index.Entries[i].Modified = now
			index.Entries[i].MessageCount = len(sess.Messages)
			index.Entries[i].Summary = sess.Summary
			index.Entries[i].LeafUUID = leafUUID
			found = true
			break
		}
	}

	if !found {
		firstPrompt := ""
		if len(sess.Messages) > 0 && sess.Messages[0].Role == session.RoleUser {
			firstPrompt = sess.Messages[0].Content
			// Truncate long prompts
			if len(firstPrompt) > 200 {
				firstPrompt = firstPrompt[:200]
			}
		}

		created := sess.CreatedAt.Format(time.RFC3339)
		if sess.CreatedAt.IsZero() {
			created = now
		}

		index.Entries = append(index.Entries, indexEntry{
			SessionID:    sessionID,
			FullPath:     filepath.Join(projDir, sessionID+jsonlExtension),
			FirstPrompt:  firstPrompt,
			Created:      created,
			Modified:     now,
			GitBranch:    sess.Branch,
			ProjectPath:  sess.ProjectPath,
			Summary:      sess.Summary,
			LeafUUID:     leafUUID,
			MessageCount: len(sess.Messages),
		})
	}

	// Write updated index
	indexData, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling sessions index: %w", err)
	}

	if err := os.WriteFile(indexPath, indexData, 0o644); err != nil {
		return fmt.Errorf("writing sessions index: %w", err)
	}

	return nil
}

// projectDir returns the Claude Code project directory for a given project path.
// Claude Code encodes paths by replacing "/" with "-".
func (p *Provider) projectDir(projectPath string) string {
	encoded := encodeProjectPath(projectPath)
	return filepath.Join(p.claudeHome, projectsDir, encoded)
}

// ListAllProjects enumerates all projects known to Claude Code by scanning
// the projects directory. Each subdirectory is a project with an encoded path.
// Implements provider.ProjectDiscoverer.
func (p *Provider) ListAllProjects() ([]provider.ProjectInfo, error) {
	projectsRoot := filepath.Join(p.claudeHome, projectsDir)
	entries, err := os.ReadDir(projectsRoot)
	if err != nil {
		return nil, fmt.Errorf("reading projects directory: %w", err)
	}

	var projects []provider.ProjectInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Decode the project path from the directory name.
		dirName := entry.Name()
		projectPath := decodeProjectPath(dirName)

		// Count sessions from the index file.
		indexPath := filepath.Join(projectsRoot, dirName, sessionsIndex)
		sessCount := 0
		if index, readErr := readSessionsIndex(indexPath); readErr == nil {
			sessCount = len(index.Entries)
		} else {
			// No index — count JSONL files instead.
			if subEntries, dirErr := os.ReadDir(filepath.Join(projectsRoot, dirName)); dirErr == nil {
				for _, se := range subEntries {
					if strings.HasSuffix(se.Name(), jsonlExtension) {
						sessCount++
					}
				}
			}
		}

		if sessCount == 0 {
			continue // skip empty project dirs
		}

		projects = append(projects, provider.ProjectInfo{
			ID:           dirName,
			Path:         projectPath,
			SessionCount: sessCount,
		})
	}

	return projects, nil
}

// decodeProjectPath converts a Claude Code directory name back to a path.
// Reverses encodeProjectPath: "-" becomes "/".
func decodeProjectPath(encoded string) string {
	return strings.ReplaceAll(encoded, "-", "/")
}

// findSessionFile locates the JSONL file for a given session ID.
// It searches all project directories.
func (p *Provider) findSessionFile(sessionID session.ID) (string, error) {
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

	return "", session.ErrSessionNotFound
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

	// image block
	Source *imageSource `json:"source,omitempty"`
}

// imageSource is the source object for image content blocks.
type imageSource struct {
	Type      string `json:"type"`       // "base64", "url"
	MediaType string `json:"media_type"` // "image/png", "image/jpeg", etc.
	Data      string `json:"data"`       // base64-encoded data (we only read length, not store)
}

// UnmarshalJSONL parses Claude Code JSONL bytes into a unified Session.
// This is a pure function with no I/O — it only deserializes data.
// It is the canonical unmarshaling logic for Claude's native format.
func UnmarshalJSONL(data []byte, mode session.StorageMode) (*session.Session, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, fmt.Errorf("empty JSONL data")
	}

	var lines []jsonlLine
	for _, lineStr := range strings.Split(trimmed, "\n") {
		if lineStr == "" {
			continue
		}
		var line jsonlLine
		if err := json.Unmarshal([]byte(lineStr), &line); err != nil {
			continue // skip malformed lines
		}
		lines = append(lines, line)
	}

	// Derive session ID from first line
	var sessionID session.ID
	for _, line := range lines {
		if line.SessionID != "" {
			sessionID = session.ID(line.SessionID)
			break
		}
	}

	return parseSession(sessionID, lines, mode)
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

func parseSession(sessionID session.ID, lines []jsonlLine, mode session.StorageMode) (*session.Session, error) {
	sess := &session.Session{
		ID:          sessionID,
		Provider:    session.ProviderClaudeCode,
		Agent:       defaultAgent,
		StorageMode: mode,
		Version:     1,
		ExportedBy:  exportedByLabel,
		ExportedAt:  time.Now(),
	}

	// Collect tool_use blocks to match with tool_results later
	toolUses := make(map[string]*session.ToolCall) // keyed by tool_use id

	// Track file changes from Write/Edit tool uses
	fileChanges := make(map[string]session.ChangeType)

	var (
		totalInput      int
		totalOutput     int
		totalCacheRead  int
		totalCacheWrite int
		totalImageToks  int
	)

	for _, line := range lines {
		// Extract metadata from first message
		if sess.Branch == "" && line.GitBranch != "" {
			sess.Branch = line.GitBranch
		}
		if sess.ProjectPath == "" && line.Cwd != "" {
			sess.ProjectPath = line.Cwd
		}

		switch line.Type {
		case "summary":
			sess.Summary = line.Summary

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
				totalCacheRead += msg.Usage.CacheReadInputTokens
				totalCacheWrite += msg.Usage.CacheCreationInputTokens
			}

			// summary mode: only metadata, no messages
			if mode == session.StorageModeSummary {
				// Still set CreatedAt from first message
				if sess.CreatedAt.IsZero() {
					sess.CreatedAt = ts
				}
				continue
			}

			domainMsg := session.Message{
				ID:        line.UUID,
				Timestamp: ts,
				Model:     msg.Model,
			}

			if msg.Role == "user" {
				domainMsg.Role = session.RoleUser
			} else {
				domainMsg.Role = session.RoleAssistant
			}

			// Parse content blocks
			parsed := parseContentBlocks(msg.Content, msg.Role, mode, toolUses, fileChanges)
			domainMsg.Content = parsed.text
			domainMsg.Thinking = parsed.thinking
			domainMsg.ToolCalls = parsed.toolCalls
			domainMsg.Images = parsed.images
			domainMsg.ContentBlocks = parsed.contentBlocks
			if msg.Usage != nil {
				domainMsg.InputTokens = msg.Usage.InputTokens + msg.Usage.CacheCreationInputTokens + msg.Usage.CacheReadInputTokens
				domainMsg.OutputTokens = msg.Usage.OutputTokens
				domainMsg.CacheReadTokens = msg.Usage.CacheReadInputTokens
				domainMsg.CacheWriteTokens = msg.Usage.CacheCreationInputTokens
			}
			// Accumulate image tokens.
			for _, img := range parsed.images {
				totalImageToks += img.TokensEstimate
			}

			// For tool_result user messages, match with pending tool_uses
			if msg.Role == "user" && len(parsed.toolResults) > 0 {
				for _, tr := range parsed.toolResults {
					if tc, ok := toolUses[tr.toolUseID]; ok {
						tc.Output = tr.output
						tc.OutputTokens = tr.outputTokens
						if tr.isError {
							tc.State = session.ToolStateError
						} else {
							tc.State = session.ToolStateCompleted
						}
					}
				}
				// Don't add tool_result-only user messages as separate messages
				// (they're merged into the tool call on the assistant message)
				if parsed.text == "" {
					continue
				}
			}

			sess.Messages = append(sess.Messages, domainMsg)

			if sess.CreatedAt.IsZero() {
				sess.CreatedAt = ts
			}
		}
	}

	// Set token usage
	sess.TokenUsage = session.TokenUsage{
		InputTokens:  totalInput,
		OutputTokens: totalOutput,
		TotalTokens:  totalInput + totalOutput,
		ImageTokens:  totalImageToks,
		CacheRead:    totalCacheRead,
		CacheWrite:   totalCacheWrite,
	}

	// Set file changes
	for path, changeType := range fileChanges {
		sess.FileChanges = append(sess.FileChanges, session.FileChange{
			FilePath:   path,
			ChangeType: changeType,
		})
	}

	return sess, nil
}

type parsedContent struct {
	text          string
	thinking      string
	toolCalls     []session.ToolCall
	toolResults   []toolResultInfo
	images        []session.ImageMeta
	contentBlocks []session.ContentBlock
	tokens        int
}

type toolResultInfo struct {
	toolUseID    string
	output       string
	outputTokens int
	isError      bool
}

// roughTokenEstimate estimates token count from byte length (~4 bytes per token).
func roughTokenEstimate(byteLen int) int {
	n := byteLen / 4
	if n == 0 && byteLen > 0 {
		n = 1
	}
	return n
}

func parseContentBlocks(raw json.RawMessage, role string, mode session.StorageMode, toolUses map[string]*session.ToolCall, fileChanges map[string]session.ChangeType) parsedContent {
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
			result.contentBlocks = append(result.contentBlocks, session.ContentBlock{
				Type: session.ContentBlockText,
				Text: block.Text,
			})

		case "thinking":
			if mode == session.StorageModeFull {
				result.thinking = block.Thinking
				result.contentBlocks = append(result.contentBlocks, session.ContentBlock{
					Type:     session.ContentBlockThinking,
					Thinking: block.Thinking,
				})
			}

		case "image":
			// Extract image metadata without storing the actual base64 data.
			img := session.ImageMeta{
				Source: "base64",
			}
			if block.Source != nil {
				img.MediaType = block.Source.MediaType
				img.Source = block.Source.Type
				if block.Source.Data != "" {
					// Compute size from base64 length (base64 is ~4/3 ratio).
					img.SizeBytes = len(block.Source.Data) * 3 / 4
					// Anthropic image token estimation:
					// ~765 tokens per 512x512 tile. Estimate from file size:
					// ~1 token per 750 bytes as a rough heuristic.
					img.TokensEstimate = img.SizeBytes / 750
					if img.TokensEstimate < 85 {
						img.TokensEstimate = 85 // minimum for any image
					}
				}
			}
			result.images = append(result.images, img)
			result.contentBlocks = append(result.contentBlocks, session.ContentBlock{
				Type:  session.ContentBlockImage,
				Image: &img,
			})

		case "tool_use":
			if mode == session.StorageModeFull || mode == session.StorageModeCompact {
				inputStr := string(block.Input)
				tc := session.ToolCall{
					ID:          block.ID,
					Name:        block.Name,
					Input:       inputStr,
					State:       session.ToolStatePending, // Will be updated when tool_result arrives
					InputTokens: roughTokenEstimate(len(inputStr)),
				}
				result.toolCalls = append(result.toolCalls, tc)
				toolUses[block.ID] = &result.toolCalls[len(result.toolCalls)-1]

				// Track file changes from Write/Edit tools
				trackFileChange(block.Name, block.Input, fileChanges)
			}

		case "tool_result":
			output := extractToolResultContent(block.Content)
			result.toolResults = append(result.toolResults, toolResultInfo{
				toolUseID:    block.ToolUseID,
				output:       output,
				outputTokens: roughTokenEstimate(len(output)),
				isError:      block.IsError,
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
func trackFileChange(toolName string, rawInput json.RawMessage, changes map[string]session.ChangeType) {
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
			changes[input.FilePath] = session.ChangeCreated
		} else {
			changes[input.FilePath] = session.ChangeModified
		}
	case "Edit":
		changes[input.FilePath] = session.ChangeModified
	case "Read":
		if _, exists := changes[input.FilePath]; !exists {
			changes[input.FilePath] = session.ChangeRead
		}
	}
}
