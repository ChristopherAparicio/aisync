// Package opencode implements the OpenCode provider for aisync.
// It reads sessions from OpenCode's file-based storage located at
// ~/.local/share/opencode/storage/ with JSON files distributed across
// project/, session/, message/, and part/ subdirectories.
package opencode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/google/uuid"
)

const (
	storageDir      = "storage"
	projectDir      = "project"
	sessionDir      = "session"
	messageDir      = "message"
	partDir         = "part"
	exportedByLabel = "aisync"
)

// Provider implements session.Provider for OpenCode.
type Provider struct {
	// dataHome overrides the default data directory (for testing).
	dataHome string
}

// New creates an OpenCode provider.
// If dataHome is empty, it defaults to the XDG data directory.
func New(dataHome string) *Provider {
	if dataHome == "" {
		dataHome = defaultDataHome()
	}
	return &Provider{dataHome: dataHome}
}

// Name returns the provider identifier.
func (p *Provider) Name() session.ProviderName {
	return session.ProviderOpenCode
}

// Detect finds sessions matching the given project and branch.
// OpenCode doesn't track branches natively, so we return all sessions
// for the matching project and let the caller filter.
func (p *Provider) Detect(projectPath string, _ string) ([]session.Summary, error) {
	projectID, err := p.findProjectID(projectPath)
	if err != nil {
		return nil, err
	}

	sessionsPath := filepath.Join(p.storagePath(), sessionDir, projectID)
	entries, err := os.ReadDir(sessionsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading sessions directory: %w", err)
	}

	var summaries []session.Summary
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, readErr := os.ReadFile(filepath.Join(sessionsPath, entry.Name()))
		if readErr != nil {
			continue
		}

		var sess ocSession
		if unmarshalErr := json.Unmarshal(data, &sess); unmarshalErr != nil {
			continue
		}

		// Skip child sessions (sub-agents) — they'll be attached as children
		if sess.ParentID != "" {
			continue
		}

		created := time.UnixMilli(sess.Time.Created)

		// Count messages for this session
		msgCount := countMessages(p.storagePath(), sess.ID)

		agent := "coder" // default OpenCode agent
		summaries = append(summaries, session.Summary{
			ID:           session.ID(sess.ID),
			Provider:     session.ProviderOpenCode,
			Agent:        agent,
			Summary:      sess.Title,
			MessageCount: msgCount,
			CreatedAt:    created,
		})
	}

	// Sort by created_at descending (most recent first)
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].CreatedAt.After(summaries[j].CreatedAt)
	})

	return summaries, nil
}

// Export reads a session and converts it to the unified Session model.
func (p *Provider) Export(sessionID session.ID, mode session.StorageMode) (*session.Session, error) {
	sess, err := p.readSession(sessionID)
	if err != nil {
		return nil, err
	}

	result := &session.Session{
		ID:          sessionID,
		Provider:    session.ProviderOpenCode,
		StorageMode: mode,
		Summary:     sess.Title,
		ProjectPath: sess.Directory,
		Version:     1,
		ExportedBy:  exportedByLabel,
		ExportedAt:  time.Now(),
		CreatedAt:   time.UnixMilli(sess.Time.Created),
	}

	// Load messages
	messages, err := p.loadMessages(string(sessionID))
	if err != nil {
		return nil, fmt.Errorf("loading messages: %w", err)
	}

	// Set agent from the first message
	for _, msg := range messages {
		if msg.Agent != "" {
			result.Agent = msg.Agent
			break
		}
	}
	if result.Agent == "" {
		result.Agent = "coder"
	}

	// Summary mode: no messages
	if mode == session.StorageModeSummary {
		result.TokenUsage = sumTokens(messages)
		return result, nil
	}

	// Build domain messages from OpenCode messages + their parts
	var (
		totalInput  int
		totalOutput int
	)

	// Sort messages by creation time
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].Time.Created < messages[j].Time.Created
	})

	for _, msg := range messages {
		parts, partErr := p.loadParts(msg.ID)
		if partErr != nil {
			continue
		}

		domainMsg := session.Message{
			ID:        msg.ID,
			Model:     msg.ModelID,
			Timestamp: time.UnixMilli(msg.Time.Created),
		}

		switch msg.Role {
		case "user":
			domainMsg.Role = session.RoleUser
		case "assistant":
			domainMsg.Role = session.RoleAssistant
		default:
			domainMsg.Role = session.RoleSystem
		}

		// Process parts
		var textParts []string
		for _, part := range parts {
			switch part.Type {
			case "text":
				textParts = append(textParts, part.Text)

			case "tool":
				if mode == session.StorageModeFull || mode == session.StorageModeCompact {
					tc := convertToolPart(part)
					domainMsg.ToolCalls = append(domainMsg.ToolCalls, tc)

					// Track file changes
					trackFileChange(part, result)
				}

			case "reasoning":
				if mode == session.StorageModeFull {
					domainMsg.Thinking = part.Text
				}
			}
		}

		domainMsg.Content = strings.Join(textParts, "\n")

		// Tokens from assistant messages
		if msg.Tokens.Input > 0 || msg.Tokens.Output > 0 {
			totalInput += msg.Tokens.Input + msg.Tokens.Cache.Read + msg.Tokens.Cache.Write
			totalOutput += msg.Tokens.Output
			domainMsg.InputTokens = msg.Tokens.Input + msg.Tokens.Cache.Read + msg.Tokens.Cache.Write
			domainMsg.OutputTokens = msg.Tokens.Output
		}

		result.Messages = append(result.Messages, domainMsg)
	}

	result.TokenUsage = session.TokenUsage{
		InputTokens:  totalInput,
		OutputTokens: totalOutput,
		TotalTokens:  totalInput + totalOutput,
	}

	// Load child sessions (sub-agents)
	children, err := p.findChildSessions(string(sessionID), mode)
	if err == nil && len(children) > 0 {
		result.Children = children
	}

	return result, nil
}

// CanImport reports that OpenCode supports session import.
func (p *Provider) CanImport() bool {
	return true
}

// Import writes a session back to OpenCode's native format.
// It creates the project, session, message, and part JSON files
// in the appropriate subdirectories under storage/.
func (p *Provider) Import(sess *session.Session) error {
	if sess == nil {
		return fmt.Errorf("session is nil")
	}

	projectPath := sess.ProjectPath
	if projectPath == "" {
		return fmt.Errorf("session has no project path")
	}

	// Generate IDs if missing
	sessionID := string(sess.ID)
	if sessionID == "" {
		sessionID = "ses_" + uuid.New().String()[:8]
		sess.ID = session.ID(sessionID)
	}

	// Step 1: Ensure or find the project
	projectID, err := p.ensureProject(projectPath)
	if err != nil {
		return fmt.Errorf("ensuring project: %w", err)
	}

	// Step 2: Write session file
	if err := p.writeSession(sess, projectID); err != nil {
		return fmt.Errorf("writing session: %w", err)
	}

	// Step 3: Write messages and their parts
	if err := p.writeMessages(sess); err != nil {
		return fmt.Errorf("writing messages: %w", err)
	}

	// Step 4: Import child sessions
	for i := range sess.Children {
		child := &sess.Children[i]
		child.ProjectPath = projectPath
		if child.ParentID == "" {
			child.ParentID = sess.ID
		}
		if err := p.Import(child); err != nil {
			return fmt.Errorf("importing child session: %w", err)
		}
	}

	return nil
}

// ensureProject finds or creates the project entry for the given path.
// Returns the project ID.
func (p *Provider) ensureProject(worktreePath string) (string, error) {
	// Try to find existing project
	existingID, err := p.findProjectID(worktreePath)
	if err == nil {
		return existingID, nil
	}

	// Create new project entry
	projectID := uuid.New().String()[:12]
	projectsPath := filepath.Join(p.storagePath(), projectDir)
	if err := os.MkdirAll(projectsPath, 0o755); err != nil {
		return "", fmt.Errorf("creating projects directory: %w", err)
	}

	proj := ocProject{
		ID:       projectID,
		Worktree: worktreePath,
	}

	data, err := json.MarshalIndent(proj, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshalling project: %w", err)
	}

	projPath := filepath.Join(projectsPath, projectID+".json")
	if err := os.WriteFile(projPath, data, 0o644); err != nil {
		return "", fmt.Errorf("writing project file: %w", err)
	}

	return projectID, nil
}

// writeSession writes the session JSON file.
func (p *Provider) writeSession(sess *session.Session, projectID string) error {
	sessDir := filepath.Join(p.storagePath(), sessionDir, projectID)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		return fmt.Errorf("creating session directory: %w", err)
	}

	created := sess.CreatedAt.UnixMilli()
	if sess.CreatedAt.IsZero() {
		created = time.Now().UnixMilli()
	}

	ocSess := ocSession{
		ID:        string(sess.ID),
		ProjectID: projectID,
		Directory: sess.ProjectPath,
		ParentID:  string(sess.ParentID),
		Title:     sess.Summary,
		Time: ocTime{
			Created: created,
			Updated: time.Now().UnixMilli(),
		},
	}

	data, err := json.MarshalIndent(ocSess, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling session: %w", err)
	}

	sessPath := filepath.Join(sessDir, string(sess.ID)+".json")
	if err := os.WriteFile(sessPath, data, 0o644); err != nil {
		return fmt.Errorf("writing session file: %w", err)
	}

	return nil
}

// writeMessages writes message and part JSON files for all messages.
func (p *Provider) writeMessages(sess *session.Session) error {
	sessionID := string(sess.ID)
	msgDir := filepath.Join(p.storagePath(), messageDir, sessionID)
	if err := os.MkdirAll(msgDir, 0o755); err != nil {
		return fmt.Errorf("creating message directory: %w", err)
	}

	for _, msg := range sess.Messages {
		msgID := msg.ID
		if msgID == "" {
			msgID = "msg_" + uuid.New().String()[:8]
		}

		created := msg.Timestamp.UnixMilli()
		if msg.Timestamp.IsZero() {
			created = time.Now().UnixMilli()
		}

		inputTokens := msg.InputTokens
		outputTokens := msg.OutputTokens

		ocMsg := ocMessage{
			ID:      msgID,
			Role:    string(msg.Role),
			Agent:   sess.Agent,
			ModelID: msg.Model,
			Model: ocModel{
				ModelID: msg.Model,
			},
			Tokens: ocTokens{
				Input:  inputTokens,
				Output: outputTokens,
			},
			Time: ocMsgTime{
				Created:   created,
				Completed: created,
			},
		}

		msgData, err := json.MarshalIndent(ocMsg, "", "  ")
		if err != nil {
			return fmt.Errorf("marshalling message: %w", err)
		}

		msgPath := filepath.Join(msgDir, msgID+".json")
		if err := os.WriteFile(msgPath, msgData, 0o644); err != nil {
			return fmt.Errorf("writing message file: %w", err)
		}

		// Write parts for this message
		if err := p.writeParts(msgID, sessionID, msg); err != nil {
			return fmt.Errorf("writing parts for message %s: %w", msgID, err)
		}
	}

	return nil
}

// writeParts writes part JSON files for a message's content and tool calls.
func (p *Provider) writeParts(msgID, sessionID string, msg session.Message) error {
	partsDir := filepath.Join(p.storagePath(), partDir, msgID)
	if err := os.MkdirAll(partsDir, 0o755); err != nil {
		return fmt.Errorf("creating parts directory: %w", err)
	}

	partIndex := 0

	// Text part
	if msg.Content != "" {
		partID := fmt.Sprintf("prt_%s_%03d", msgID, partIndex)
		part := ocPart{
			ID:        partID,
			SessionID: sessionID,
			MessageID: msgID,
			Type:      "text",
			Text:      msg.Content,
		}

		data, err := json.MarshalIndent(part, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(partsDir, partID+".json"), data, 0o644); err != nil {
			return err
		}
		partIndex++
	}

	// Thinking/reasoning part
	if msg.Thinking != "" {
		partID := fmt.Sprintf("prt_%s_%03d", msgID, partIndex)
		part := ocPart{
			ID:        partID,
			SessionID: sessionID,
			MessageID: msgID,
			Type:      "reasoning",
			Text:      msg.Thinking,
		}

		data, err := json.MarshalIndent(part, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(partsDir, partID+".json"), data, 0o644); err != nil {
			return err
		}
		partIndex++
	}

	// Tool call parts
	for _, tc := range msg.ToolCalls {
		partID := tc.ID
		if partID == "" {
			partID = fmt.Sprintf("prt_%s_%03d", msgID, partIndex)
		}

		var input interface{}
		if tc.Input != "" && json.Valid([]byte(tc.Input)) {
			_ = json.Unmarshal([]byte(tc.Input), &input)
		}

		status := string(tc.State)
		if status == "" {
			status = "completed"
		}

		part := ocPart{
			ID:        partID,
			SessionID: sessionID,
			MessageID: msgID,
			Type:      "tool",
			CallID:    tc.ID,
			Tool:      tc.Name,
			State: ocToolState{
				Input:  input,
				Output: tc.Output,
				Status: status,
				Time: ocPartTime{
					Start: msg.Timestamp.UnixMilli(),
					End:   msg.Timestamp.UnixMilli() + int64(tc.DurationMs),
				},
			},
		}

		data, err := json.MarshalIndent(part, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(partsDir, partID+".json"), data, 0o644); err != nil {
			return err
		}
		partIndex++
	}

	return nil
}

// --- Marshal / Unmarshal (pure, no I/O) ---

// ExportBundle is a single-JSON representation of an OpenCode session.
// It bundles session + messages + parts into one object, suitable for
// cross-provider conversion or serialization. This is the canonical
// marshaling format for OpenCode's native data.
type ExportBundle struct {
	Messages []ExportMsg   `json:"messages"`
	Session  ExportSession `json:"session"`
}

// ExportSession is the session metadata in an ExportBundle.
type ExportSession struct {
	ID        string          `json:"id"`
	ProjectID string          `json:"projectID"`
	Directory string          `json:"directory"`
	ParentID  string          `json:"parentID,omitempty"`
	Title     string          `json:"title"`
	Time      ExportTimestamp `json:"time"`
}

// ExportTimestamp holds created/updated millisecond timestamps.
type ExportTimestamp struct {
	Created int64 `json:"created"`
	Updated int64 `json:"updated"`
}

// ExportMsg is a message in an ExportBundle.
type ExportMsg struct {
	ID      string        `json:"id"`
	Role    string        `json:"role"`
	Agent   string        `json:"agent,omitempty"`
	ModelID string        `json:"modelID,omitempty"`
	Content string        `json:"content,omitempty"`
	Parts   []ExportPart  `json:"parts"`
	Tokens  ExportTokens  `json:"tokens"`
	Time    ExportMsgTime `json:"time"`
}

// ExportMsgTime holds message timing.
type ExportMsgTime struct {
	Created   int64 `json:"created"`
	Completed int64 `json:"completed"`
}

// ExportPart is a message part in an ExportBundle.
type ExportPart struct {
	State *ExportState `json:"state,omitempty"`
	ID    string       `json:"id"`
	Type  string       `json:"type"`
	Text  string       `json:"text,omitempty"`
	Tool  string       `json:"tool,omitempty"`
}

// ExportState holds tool execution state.
type ExportState struct {
	Input  interface{} `json:"input,omitempty"`
	Output string      `json:"output,omitempty"`
	Status string      `json:"status"`
}

// ExportTokens holds token counts for a message.
type ExportTokens struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// MarshalJSON converts a unified Session to an OpenCode ExportBundle JSON.
// This is a pure function with no I/O — it only serializes data.
func MarshalJSON(sess *session.Session) ([]byte, error) {
	export := ExportBundle{
		Session: ExportSession{
			ID:        string(sess.ID),
			Directory: sess.ProjectPath,
			Title:     sess.Summary,
			Time: ExportTimestamp{
				Created: sess.CreatedAt.UnixMilli(),
				Updated: sess.ExportedAt.UnixMilli(),
			},
		},
	}

	if sess.ParentID != "" {
		export.Session.ParentID = string(sess.ParentID)
	}

	for _, msg := range sess.Messages {
		ocMsg := ExportMsg{
			ID:      msg.ID,
			Role:    string(msg.Role),
			Agent:   sess.Agent,
			ModelID: msg.Model,
			Content: msg.Content,
			Tokens: ExportTokens{
				Input:  msg.InputTokens,
				Output: msg.OutputTokens,
			},
			Time: ExportMsgTime{
				Created:   msg.Timestamp.UnixMilli(),
				Completed: msg.Timestamp.UnixMilli(),
			},
		}

		// Text part
		if msg.Content != "" {
			ocMsg.Parts = append(ocMsg.Parts, ExportPart{
				ID:   msg.ID + "-text",
				Type: "text",
				Text: msg.Content,
			})
		}

		// Thinking part
		if msg.Thinking != "" {
			ocMsg.Parts = append(ocMsg.Parts, ExportPart{
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
			ocMsg.Parts = append(ocMsg.Parts, ExportPart{
				ID:   tc.ID,
				Type: "tool",
				Tool: tc.Name,
				State: &ExportState{
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

// UnmarshalJSON parses an OpenCode ExportBundle JSON into a unified Session.
// This is a pure function with no I/O — it only deserializes data.
func UnmarshalJSON(data []byte) (*session.Session, error) {
	var export ExportBundle
	if err := json.Unmarshal(data, &export); err != nil {
		return nil, fmt.Errorf("parsing OpenCode JSON: %w", err)
	}

	sess := &session.Session{
		ID:          session.ID(export.Session.ID),
		Provider:    session.ProviderOpenCode,
		Agent:       "coder",
		StorageMode: session.StorageModeFull,
		Summary:     export.Session.Title,
		ProjectPath: export.Session.Directory,
		Version:     1,
		ExportedBy:  "aisync-import",
		ExportedAt:  time.Now(),
		CreatedAt:   time.UnixMilli(export.Session.Time.Created),
	}

	if export.Session.ParentID != "" {
		sess.ParentID = session.ID(export.Session.ParentID)
	}

	var totalInput, totalOutput int
	for _, msg := range export.Messages {
		if msg.Agent != "" {
			sess.Agent = msg.Agent
		}

		domainMsg := session.Message{
			ID:        msg.ID,
			Model:     msg.ModelID,
			Timestamp: time.UnixMilli(msg.Time.Created),
		}

		switch msg.Role {
		case "user":
			domainMsg.Role = session.RoleUser
		case "assistant":
			domainMsg.Role = session.RoleAssistant
		default:
			domainMsg.Role = session.RoleSystem
		}

		totalInput += msg.Tokens.Input
		totalOutput += msg.Tokens.Output
		domainMsg.InputTokens = msg.Tokens.Input
		domainMsg.OutputTokens = msg.Tokens.Output

		// Process parts
		var textParts []string
		for _, part := range msg.Parts {
			switch part.Type {
			case "text":
				textParts = append(textParts, part.Text)
			case "reasoning":
				domainMsg.Thinking = part.Text
			case "tool":
				tc := session.ToolCall{
					ID:   part.ID,
					Name: part.Tool,
				}
				if part.State != nil {
					tc.Output = part.State.Output
					tc.OutputTokens = roughTokenEstimate(len(tc.Output))
					switch part.State.Status {
					case "completed":
						tc.State = session.ToolStateCompleted
					case "error":
						tc.State = session.ToolStateError
					case "running":
						tc.State = session.ToolStateRunning
					default:
						tc.State = session.ToolStatePending
					}
					if part.State.Input != nil {
						inputBytes, _ := json.Marshal(part.State.Input)
						tc.Input = string(inputBytes)
						tc.InputTokens = roughTokenEstimate(len(tc.Input))
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

		sess.Messages = append(sess.Messages, domainMsg)
	}

	sess.TokenUsage = session.TokenUsage{
		InputTokens:  totalInput,
		OutputTokens: totalOutput,
		TotalTokens:  totalInput + totalOutput,
	}

	if sess.ID == "" {
		sess.ID = session.NewID()
	}

	return sess, nil
}

// --- Internal helpers ---

func (p *Provider) storagePath() string {
	return filepath.Join(p.dataHome, storageDir)
}

// findProjectID finds the project ID for a given worktree path.
// The project ID is the first root commit hash of the git repository,
// cached in the project JSON files.
func (p *Provider) findProjectID(worktreePath string) (string, error) {
	projectsPath := filepath.Join(p.storagePath(), projectDir)
	entries, err := os.ReadDir(projectsPath)
	if err != nil {
		return "", fmt.Errorf("reading projects directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if entry.Name() == "global.json" {
			continue
		}

		data, readErr := os.ReadFile(filepath.Join(projectsPath, entry.Name()))
		if readErr != nil {
			continue
		}

		var proj ocProject
		if unmarshalErr := json.Unmarshal(data, &proj); unmarshalErr != nil {
			continue
		}

		if proj.Worktree == worktreePath {
			return proj.ID, nil
		}
	}

	return "", session.ErrSessionNotFound
}

func (p *Provider) readSession(sessionID session.ID) (*ocSession, error) {
	// Find the session file across all project directories
	sessionsRoot := filepath.Join(p.storagePath(), sessionDir)
	projectDirs, err := os.ReadDir(sessionsRoot)
	if err != nil {
		return nil, fmt.Errorf("reading session root: %w", err)
	}

	fileName := string(sessionID) + ".json"
	for _, dir := range projectDirs {
		if !dir.IsDir() {
			continue
		}
		path := filepath.Join(sessionsRoot, dir.Name(), fileName)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}

		var sess ocSession
		if unmarshalErr := json.Unmarshal(data, &sess); unmarshalErr != nil {
			continue
		}
		return &sess, nil
	}

	return nil, session.ErrSessionNotFound
}

func (p *Provider) loadMessages(sessionID string) ([]ocMessage, error) {
	messagesPath := filepath.Join(p.storagePath(), messageDir, sessionID)
	entries, err := os.ReadDir(messagesPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading messages directory: %w", err)
	}

	var messages []ocMessage
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, readErr := os.ReadFile(filepath.Join(messagesPath, entry.Name()))
		if readErr != nil {
			continue
		}

		var msg ocMessage
		if unmarshalErr := json.Unmarshal(data, &msg); unmarshalErr != nil {
			continue
		}
		messages = append(messages, msg)
	}

	return messages, nil
}

func (p *Provider) loadParts(messageID string) ([]ocPart, error) {
	partsPath := filepath.Join(p.storagePath(), partDir, messageID)
	entries, err := os.ReadDir(partsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading parts directory: %w", err)
	}

	var parts []ocPart
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, readErr := os.ReadFile(filepath.Join(partsPath, entry.Name()))
		if readErr != nil {
			continue
		}

		var part ocPart
		if unmarshalErr := json.Unmarshal(data, &part); unmarshalErr != nil {
			continue
		}
		parts = append(parts, part)
	}

	return parts, nil
}

func (p *Provider) findChildSessions(parentID string, mode session.StorageMode) ([]session.Session, error) {
	// Search all project session directories for sessions with this parentID
	sessionsRoot := filepath.Join(p.storagePath(), sessionDir)
	projectDirs, err := os.ReadDir(sessionsRoot)
	if err != nil {
		return nil, err
	}

	var children []session.Session
	for _, dir := range projectDirs {
		if !dir.IsDir() {
			continue
		}
		dirPath := filepath.Join(sessionsRoot, dir.Name())
		entries, readErr := os.ReadDir(dirPath)
		if readErr != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}

			data, fileErr := os.ReadFile(filepath.Join(dirPath, entry.Name()))
			if fileErr != nil {
				continue
			}

			var sess ocSession
			if unmarshalErr := json.Unmarshal(data, &sess); unmarshalErr != nil {
				continue
			}

			if sess.ParentID == parentID {
				// Recursively export the child
				child, exportErr := p.Export(session.ID(sess.ID), mode)
				if exportErr != nil {
					continue
				}
				child.ParentID = session.ID(parentID)
				children = append(children, *child)
			}
		}
	}

	return children, nil
}

func countMessages(storagePath, sessionID string) int {
	messagesPath := filepath.Join(storagePath, messageDir, sessionID)
	entries, err := os.ReadDir(messagesPath)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			count++
		}
	}
	return count
}

func convertToolPart(part ocPart) session.ToolCall {
	tc := session.ToolCall{
		ID:   part.CallID,
		Name: part.Tool,
	}

	// Extract input as JSON string
	if part.State.Input != nil {
		inputBytes, _ := json.Marshal(part.State.Input)
		tc.Input = string(inputBytes)
	}

	tc.Output = part.State.Output

	// Estimate tokens from content size (~4 bytes per token).
	tc.InputTokens = roughTokenEstimate(len(tc.Input))
	tc.OutputTokens = roughTokenEstimate(len(tc.Output))

	// Map status to domain ToolState
	switch part.State.Status {
	case "completed":
		tc.State = session.ToolStateCompleted
	case "error":
		tc.State = session.ToolStateError
	case "running":
		tc.State = session.ToolStateRunning
	default:
		tc.State = session.ToolStatePending
	}

	// Duration
	if part.State.Time.Start > 0 && part.State.Time.End > 0 {
		tc.DurationMs = int(part.State.Time.End - part.State.Time.Start)
	}

	return tc
}

// roughTokenEstimate estimates token count from byte length (~4 bytes per token).
func roughTokenEstimate(byteLen int) int {
	n := byteLen / 4
	if n == 0 && byteLen > 0 {
		n = 1
	}
	return n
}

func trackFileChange(part ocPart, sess *session.Session) {
	if part.Type != "tool" {
		return
	}

	// Try to extract file_path from the tool input
	if part.State.Input == nil {
		return
	}

	var input struct {
		FilePath string `json:"file_path"`
	}
	inputBytes, _ := json.Marshal(part.State.Input)
	if err := json.Unmarshal(inputBytes, &input); err != nil || input.FilePath == "" {
		return
	}

	changeType := session.ChangeRead
	switch strings.ToLower(part.Tool) {
	case "write":
		changeType = session.ChangeCreated
	case "edit":
		changeType = session.ChangeModified
	}

	// Avoid duplicates
	for _, fc := range sess.FileChanges {
		if fc.FilePath == input.FilePath {
			return
		}
	}

	sess.FileChanges = append(sess.FileChanges, session.FileChange{
		FilePath:   input.FilePath,
		ChangeType: changeType,
	})
}

func sumTokens(messages []ocMessage) session.TokenUsage {
	var input, output int
	for _, msg := range messages {
		input += msg.Tokens.Input + msg.Tokens.Cache.Read + msg.Tokens.Cache.Write
		output += msg.Tokens.Output
	}
	return session.TokenUsage{
		InputTokens:  input,
		OutputTokens: output,
		TotalTokens:  input + output,
	}
}

// defaultDataHome returns the XDG data directory for OpenCode.
func defaultDataHome() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "opencode")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "opencode")
}

// --- JSON structures for OpenCode's native format ---

type ocProject struct {
	ID       string `json:"id"`
	Worktree string `json:"worktree"`
}

type ocSession struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectID"`
	Directory string `json:"directory"`
	ParentID  string `json:"parentID"`
	Title     string `json:"title"`
	Time      ocTime `json:"time"`
}

type ocTime struct {
	Created int64 `json:"created"`
	Updated int64 `json:"updated"`
}

type ocMessage struct {
	Model   ocModel   `json:"model"`
	ID      string    `json:"id"`
	Role    string    `json:"role"`
	Agent   string    `json:"agent"`
	ModelID string    `json:"modelID"`
	Tokens  ocTokens  `json:"tokens"`
	Time    ocMsgTime `json:"time"`
}

type ocMsgTime struct {
	Created   int64 `json:"created"`
	Completed int64 `json:"completed"`
}

type ocModel struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
}

type ocTokens struct {
	Cache     ocCache `json:"cache"`
	Input     int     `json:"input"`
	Output    int     `json:"output"`
	Reasoning int     `json:"reasoning"`
}

type ocCache struct {
	Read  int `json:"read"`
	Write int `json:"write"`
}

type ocPart struct {
	ID        string      `json:"id"`
	SessionID string      `json:"sessionID"`
	MessageID string      `json:"messageID"`
	Type      string      `json:"type"`
	Text      string      `json:"text"`
	CallID    string      `json:"callID"`
	Tool      string      `json:"tool"`
	State     ocToolState `json:"state"`
}

type ocToolState struct {
	Input    interface{} `json:"input"`
	Metadata interface{} `json:"metadata"`
	Status   string      `json:"status"`
	Output   string      `json:"output"`
	Title    string      `json:"title"`
	Time     ocPartTime  `json:"time"`
}

type ocPartTime struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}
