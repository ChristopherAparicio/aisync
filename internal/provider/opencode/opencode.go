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

	"github.com/ChristopherAparicio/aisync/internal/domain"
)

const (
	storageDir      = "storage"
	projectDir      = "project"
	sessionDir      = "session"
	messageDir      = "message"
	partDir         = "part"
	exportedByLabel = "aisync"
)

// Provider implements domain.Provider for OpenCode.
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
func (p *Provider) Name() domain.ProviderName {
	return domain.ProviderOpenCode
}

// Detect finds sessions matching the given project and branch.
// OpenCode doesn't track branches natively, so we return all sessions
// for the matching project and let the caller filter.
func (p *Provider) Detect(projectPath string, _ string) ([]domain.SessionSummary, error) {
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

	var summaries []domain.SessionSummary
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
		summaries = append(summaries, domain.SessionSummary{
			ID:           domain.SessionID(sess.ID),
			Provider:     domain.ProviderOpenCode,
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
func (p *Provider) Export(sessionID domain.SessionID, mode domain.StorageMode) (*domain.Session, error) {
	sess, err := p.readSession(sessionID)
	if err != nil {
		return nil, err
	}

	session := &domain.Session{
		ID:          sessionID,
		Provider:    domain.ProviderOpenCode,
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
			session.Agent = msg.Agent
			break
		}
	}
	if session.Agent == "" {
		session.Agent = "coder"
	}

	// Summary mode: no messages
	if mode == domain.StorageModeSummary {
		session.TokenUsage = sumTokens(messages)
		return session, nil
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

		// Process parts
		var textParts []string
		for _, part := range parts {
			switch part.Type {
			case "text":
				textParts = append(textParts, part.Text)

			case "tool":
				if mode == domain.StorageModeFull || mode == domain.StorageModeCompact {
					tc := convertToolPart(part)
					domainMsg.ToolCalls = append(domainMsg.ToolCalls, tc)

					// Track file changes
					trackFileChange(part, session)
				}

			case "reasoning":
				if mode == domain.StorageModeFull {
					domainMsg.Thinking = part.Text
				}
			}
		}

		domainMsg.Content = strings.Join(textParts, "\n")

		// Tokens from assistant messages
		if msg.Tokens.Input > 0 || msg.Tokens.Output > 0 {
			totalInput += msg.Tokens.Input + msg.Tokens.Cache.Read + msg.Tokens.Cache.Write
			totalOutput += msg.Tokens.Output
			domainMsg.Tokens = msg.Tokens.Input + msg.Tokens.Output
		}

		session.Messages = append(session.Messages, domainMsg)
	}

	session.TokenUsage = domain.TokenUsage{
		InputTokens:  totalInput,
		OutputTokens: totalOutput,
		TotalTokens:  totalInput + totalOutput,
	}

	// Load child sessions (sub-agents)
	children, err := p.findChildSessions(string(sessionID), mode)
	if err == nil && len(children) > 0 {
		session.Children = children
	}

	return session, nil
}

// CanImport reports that OpenCode supports session import.
func (p *Provider) CanImport() bool {
	return true
}

// Import writes a session back to OpenCode's native format.
func (p *Provider) Import(session *domain.Session) error {
	// TODO: Implement in Milestone 1.6 (Export/Import)
	return domain.ErrImportNotSupported
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

	return "", domain.ErrSessionNotFound
}

func (p *Provider) readSession(sessionID domain.SessionID) (*ocSession, error) {
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

	return nil, domain.ErrSessionNotFound
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

func (p *Provider) findChildSessions(parentID string, mode domain.StorageMode) ([]domain.Session, error) {
	// Search all project session directories for sessions with this parentID
	sessionsRoot := filepath.Join(p.storagePath(), sessionDir)
	projectDirs, err := os.ReadDir(sessionsRoot)
	if err != nil {
		return nil, err
	}

	var children []domain.Session
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
				child, exportErr := p.Export(domain.SessionID(sess.ID), mode)
				if exportErr != nil {
					continue
				}
				child.ParentID = domain.SessionID(parentID)
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

func convertToolPart(part ocPart) domain.ToolCall {
	tc := domain.ToolCall{
		ID:   part.CallID,
		Name: part.Tool,
	}

	// Extract input as JSON string
	if part.State.Input != nil {
		inputBytes, _ := json.Marshal(part.State.Input)
		tc.Input = string(inputBytes)
	}

	tc.Output = part.State.Output

	// Map status to domain ToolState
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

	// Duration
	if part.State.Time.Start > 0 && part.State.Time.End > 0 {
		tc.DurationMs = int(part.State.Time.End - part.State.Time.Start)
	}

	return tc
}

func trackFileChange(part ocPart, session *domain.Session) {
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

	changeType := domain.ChangeRead
	switch strings.ToLower(part.Tool) {
	case "write":
		changeType = domain.ChangeCreated
	case "edit":
		changeType = domain.ChangeModified
	}

	// Avoid duplicates
	for _, fc := range session.FileChanges {
		if fc.FilePath == input.FilePath {
			return
		}
	}

	session.FileChanges = append(session.FileChanges, domain.FileChange{
		FilePath:   input.FilePath,
		ChangeType: changeType,
	})
}

func sumTokens(messages []ocMessage) domain.TokenUsage {
	var input, output int
	for _, msg := range messages {
		input += msg.Tokens.Input + msg.Tokens.Cache.Read + msg.Tokens.Cache.Write
		output += msg.Tokens.Output
	}
	return domain.TokenUsage{
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
