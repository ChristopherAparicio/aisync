// Package opencode implements the OpenCode provider for aisync.
// It reads sessions from OpenCode's SQLite database (opencode.db) or falls
// back to the legacy file-based storage at ~/.local/share/opencode/storage/
// when the database is unavailable.
package opencode

import (
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
	// reader abstracts the storage backend (SQLite DB or JSON files).
	reader reader
}

// New creates an OpenCode provider.
// If dataHome is empty, it defaults to the XDG data directory.
// It tries to open the SQLite database first, falling back to file-based reading.
func New(dataHome string) *Provider {
	if dataHome == "" {
		dataHome = defaultDataHome()
	}
	p := &Provider{dataHome: dataHome}

	// Try DB reader first; fall back to file reader.
	if dbr, err := newDBReader(dataHome); err == nil {
		p.reader = dbr
	} else {
		p.reader = newFileReader(dataHome)
	}

	return p
}

// Name returns the provider identifier.
func (p *Provider) Name() session.ProviderName {
	return session.ProviderOpenCode
}

// SessionFreshness returns the message count and last-updated timestamp
// for a session, enabling the skip-if-unchanged optimization.
// Implements provider.FreshnessChecker.
func (p *Provider) SessionFreshness(sessionID session.ID) (*provider.Freshness, error) {
	count := p.reader.countMessages(string(sessionID))
	updated := p.reader.sessionUpdatedAt(string(sessionID))
	if count == 0 && updated == 0 {
		return nil, session.ErrSessionNotFound
	}
	return &provider.Freshness{
		MessageCount: count,
		UpdatedAt:    updated,
	}, nil
}

// Detect finds sessions matching the given project and branch.
// OpenCode doesn't track branches natively, so we return all sessions
// for the matching project and let the caller filter.
func (p *Provider) Detect(projectPath string, _ string) ([]session.Summary, error) {
	projectID, err := p.reader.findProjectID(projectPath)
	if err != nil {
		return nil, err
	}

	sessions, err := p.reader.listSessions(projectID)
	if err != nil {
		return nil, err
	}

	var summaries []session.Summary
	for _, sess := range sessions {
		// Skip child sessions (sub-agents) — they'll be attached as children.
		if sess.ParentID != "" {
			continue
		}

		created := time.UnixMilli(sess.Time.Created)
		msgCount := p.reader.countMessages(sess.ID)

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

	// Sort by created_at descending (most recent first).
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].CreatedAt.After(summaries[j].CreatedAt)
	})

	return summaries, nil
}

// Export reads a session and converts it to the unified Session model.
func (p *Provider) Export(sessionID session.ID, mode session.StorageMode) (*session.Session, error) {
	sess, err := p.reader.readSession(string(sessionID))
	if err != nil {
		return nil, err
	}

	result := &session.Session{
		ID:              sessionID,
		Provider:        session.ProviderOpenCode,
		StorageMode:     mode,
		Summary:         sess.Title,
		ProjectPath:     sess.Directory,
		Version:         1,
		ExportedBy:      exportedByLabel,
		ExportedAt:      time.Now(),
		CreatedAt:       time.UnixMilli(sess.Time.Created),
		SourceUpdatedAt: sess.Time.Updated,
	}

	// Load messages.
	messages, err := p.reader.loadMessages(string(sessionID))
	if err != nil {
		return nil, fmt.Errorf("loading messages: %w", err)
	}

	// Set agent from the first message.
	for _, msg := range messages {
		if msg.Agent != "" {
			result.Agent = msg.Agent
			break
		}
	}
	if result.Agent == "" {
		result.Agent = "coder"
	}

	// Summary mode: no messages.
	if mode == session.StorageModeSummary {
		result.TokenUsage = sumTokens(messages)
		return result, nil
	}

	// Build domain messages from OpenCode messages + their parts.
	var (
		totalInput      int
		totalOutput     int
		totalCacheRead  int
		totalCacheWrite int
	)

	// Sort messages by creation time.
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].Time.Created < messages[j].Time.Created
	})

	// Batch-load ALL parts for this session in one query (fixes N+1 problem).
	allParts, batchErr := p.reader.loadAllPartsForSession(string(result.ID))
	if batchErr != nil {
		allParts = nil // fallback to per-message loading below
	}

	for _, msg := range messages {
		var parts []ocPart
		if allParts != nil {
			parts = allParts[msg.ID]
		} else {
			// Fallback: per-message loading (for file reader or if batch failed).
			parts, _ = p.reader.loadParts(msg.ID)
		}

		// Resolve providerID: top-level field takes precedence, fallback to nested model.
		providerID := msg.ProviderID
		if providerID == "" {
			providerID = msg.Model.ProviderID
		}

		domainMsg := session.Message{
			ID:           msg.ID,
			Model:        msg.ModelID,
			ProviderID:   providerID,
			ProviderCost: msg.Cost,
			Timestamp:    time.UnixMilli(msg.Time.Created),
		}

		switch msg.Role {
		case "user":
			domainMsg.Role = session.RoleUser
		case "assistant":
			domainMsg.Role = session.RoleAssistant
		default:
			domainMsg.Role = session.RoleSystem
		}

		// Process parts.
		var textParts []string
		for _, part := range parts {
			switch part.Type {
			case "text":
				textParts = append(textParts, part.Text)

			case "tool":
				if mode == session.StorageModeFull || mode == session.StorageModeCompact {
					tc := convertToolPart(part)
					domainMsg.ToolCalls = append(domainMsg.ToolCalls, tc)

					// Track file changes.
					trackFileChange(part, result)
				}

			case "reasoning":
				if mode == session.StorageModeFull {
					domainMsg.Thinking = part.Text
				}

			case "image", "file":
				// Image parts: extract metadata (don't store actual image data).
				if part.MediaType != "" && isImageMediaType(part.MediaType) {
					img := session.ImageMeta{
						MediaType: part.MediaType,
						Source:    part.Type,
						FileName:  part.FileName,
					}
					if part.DataLen > 0 {
						img.SizeBytes = part.DataLen
						img.TokensEstimate = part.DataLen / 750
						if img.TokensEstimate < 85 {
							img.TokensEstimate = 85
						}
					}
					domainMsg.Images = append(domainMsg.Images, img)
				}
			}
		}

		domainMsg.Content = strings.Join(textParts, "\n")

		// Tokens from assistant messages.
		if msg.Tokens.Input > 0 || msg.Tokens.Output > 0 {
			// Separate raw input from cache tokens for accurate cost estimation.
			// Cache reads are 10x cheaper than raw input at API pricing.
			rawInput := msg.Tokens.Input
			cacheRead := msg.Tokens.Cache.Read
			cacheWrite := msg.Tokens.Cache.Write
			totalInput += rawInput
			totalCacheRead += cacheRead
			totalCacheWrite += cacheWrite
			totalOutput += msg.Tokens.Output
			// Per-message: store the total (raw + cache) for display purposes.
			domainMsg.InputTokens = rawInput + cacheRead + cacheWrite
			domainMsg.OutputTokens = msg.Tokens.Output
			domainMsg.CacheReadTokens = cacheRead
			domainMsg.CacheWriteTokens = cacheWrite
		}

		result.Messages = append(result.Messages, domainMsg)
	}

	result.TokenUsage = session.TokenUsage{
		InputTokens:  totalInput + totalCacheRead + totalCacheWrite,
		OutputTokens: totalOutput,
		TotalTokens:  totalInput + totalCacheRead + totalCacheWrite + totalOutput,
		CacheRead:    totalCacheRead,
		CacheWrite:   totalCacheWrite,
	}

	// Extract structured errors from messages.
	result.Errors = ExtractErrors(sessionID, messages, result.Messages)

	// Load child sessions (sub-agents).
	children, err := p.loadChildSessionsFull(string(sessionID), mode)
	if err == nil && len(children) > 0 {
		result.Children = children
	}

	return result, nil
}

// loadChildSessionsFull recursively exports child sessions.
func (p *Provider) loadChildSessionsFull(parentID string, mode session.StorageMode) ([]session.Session, error) {
	childSessions, err := p.reader.findChildSessions(parentID)
	if err != nil {
		return nil, err
	}

	var children []session.Session
	for _, cs := range childSessions {
		child, exportErr := p.Export(session.ID(cs.ID), mode)
		if exportErr != nil {
			continue
		}
		child.ParentID = session.ID(parentID)
		children = append(children, *child)
	}
	return children, nil
}

// CanImport reports that OpenCode supports session import.
func (p *Provider) CanImport() bool {
	return true
}

// Import writes a session back to OpenCode's native format.
// When OpenCode's SQLite database is available, it writes directly to the DB
// so the session appears immediately. Otherwise, it falls back to writing
// JSON files under storage/ (legacy path).
func (p *Provider) Import(sess *session.Session) error {
	if sess == nil {
		return fmt.Errorf("session is nil")
	}

	projectPath := sess.ProjectPath
	if projectPath == "" {
		return fmt.Errorf("session has no project path")
	}

	// Generate IDs if missing.
	sessionID := string(sess.ID)
	if sessionID == "" {
		sessionID = "ses_" + uuid.New().String()[:8]
		sess.ID = session.ID(sessionID)
	}

	// Try DB writer first — this is the primary path for modern OpenCode.
	if dw, err := newDBWriter(p.dataHome); err == nil {
		defer dw.close()
		if err := p.importViaDB(dw, sess); err != nil {
			return fmt.Errorf("DB import: %w", err)
		}
		return nil
	}

	// Fallback to file-based writer (legacy).
	return p.importViaFiles(sess)
}

// importViaDB writes the session tree directly into OpenCode's SQLite DB.
func (p *Provider) importViaDB(dw *dbWriter, sess *session.Session) error {
	if err := dw.importSession(sess, sess.Agent); err != nil {
		return err
	}

	// Recursively import child sessions.
	for i := range sess.Children {
		child := &sess.Children[i]
		child.ProjectPath = sess.ProjectPath
		if child.ParentID == "" {
			child.ParentID = sess.ID
		}
		if err := dw.importSession(child, child.Agent); err != nil {
			return fmt.Errorf("importing child session: %w", err)
		}
	}
	return nil
}

// importViaFiles writes JSON files under storage/ (legacy path).
func (p *Provider) importViaFiles(sess *session.Session) error {
	// Step 1: Ensure or find the project.
	projectID, err := p.ensureProject(sess.ProjectPath)
	if err != nil {
		return fmt.Errorf("ensuring project: %w", err)
	}

	// Step 2: Write session file.
	if err := p.writeSession(sess, projectID); err != nil {
		return fmt.Errorf("writing session: %w", err)
	}

	// Step 3: Write messages and their parts.
	if err := p.writeMessages(sess); err != nil {
		return fmt.Errorf("writing messages: %w", err)
	}

	// Step 4: Import child sessions.
	for i := range sess.Children {
		child := &sess.Children[i]
		child.ProjectPath = sess.ProjectPath
		if child.ParentID == "" {
			child.ParentID = sess.ID
		}
		if err := p.importViaFiles(child); err != nil {
			return fmt.Errorf("importing child session: %w", err)
		}
	}

	return nil
}

// ensureProject finds or creates the project entry for the given path.
// Returns the project ID.
func (p *Provider) ensureProject(worktreePath string) (string, error) {
	// Try to find existing project via file reader (import always uses files).
	fr := newFileReader(p.dataHome)
	existingID, err := fr.findProjectID(worktreePath)
	if err == nil {
		return existingID, nil
	}

	// Create new project entry.
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

		// Write parts for this message.
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

	// Text part.
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

	// Thinking/reasoning part.
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

	// Tool call parts.
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

		// Text part.
		if msg.Content != "" {
			ocMsg.Parts = append(ocMsg.Parts, ExportPart{
				ID:   msg.ID + "-text",
				Type: "text",
				Text: msg.Content,
			})
		}

		// Thinking part.
		if msg.Thinking != "" {
			ocMsg.Parts = append(ocMsg.Parts, ExportPart{
				ID:   msg.ID + "-reasoning",
				Type: "reasoning",
				Text: msg.Thinking,
			})
		}

		// Tool call parts.
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

		// Process parts.
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

func convertToolPart(part ocPart) session.ToolCall {
	tc := session.ToolCall{
		ID:   part.CallID,
		Name: part.Tool,
	}

	// Extract input as JSON string.
	if part.State.Input != nil {
		inputBytes, _ := json.Marshal(part.State.Input)
		tc.Input = string(inputBytes)
	}

	tc.Output = part.State.Output

	// Estimate tokens from content size (~4 bytes per token).
	tc.InputTokens = roughTokenEstimate(len(tc.Input))
	tc.OutputTokens = roughTokenEstimate(len(tc.Output))

	// Map status to domain ToolState.
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

	// Duration.
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

	// Try to extract file_path from the tool input.
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

	// Avoid duplicates.
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
	Model      ocModel     `json:"model"`
	ID         string      `json:"id"`
	Role       string      `json:"role"`
	Agent      string      `json:"agent"`
	ModelID    string      `json:"modelID"`
	ProviderID string      `json:"providerID"` // e.g. "anthropic", "amazon-bedrock", "opencode"
	Tokens     ocTokens    `json:"tokens"`
	Time       ocMsgTime   `json:"time"`
	Cost       float64     `json:"cost"`  // actual cost reported by provider (0 for subscriptions)
	Error      *ocAPIError `json:"error"` // API-level error (e.g. HTTP 500 from Anthropic)
}

// ocAPIError represents an API-level error captured by OpenCode.
// This is NOT a tool error — it's an HTTP error from the provider API.
type ocAPIError struct {
	Name string         `json:"name"` // e.g. "APIError"
	Data ocAPIErrorData `json:"data"`
}

// ocAPIErrorData holds the detailed error data from the provider.
type ocAPIErrorData struct {
	Message         string            `json:"message"`    // e.g. "Internal server error"
	StatusCode      int               `json:"statusCode"` // HTTP status code
	IsRetryable     bool              `json:"isRetryable"`
	ResponseHeaders map[string]string `json:"responseHeaders"` // rate limit headers, request-id, etc.
	ResponseBody    string            `json:"responseBody"`    // raw response body
	Metadata        map[string]string `json:"metadata"`        // e.g. {"url": "https://api.anthropic.com/v1/messages"}
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
	Type      string      `json:"type"` // "text", "tool", "reasoning", "image", "file"
	Text      string      `json:"text"`
	CallID    string      `json:"callID"`
	Tool      string      `json:"tool"`
	State     ocToolState `json:"state"`
	MediaType string      `json:"mediaType,omitempty"` // e.g. "image/png" for image/file parts
	FileName  string      `json:"filename,omitempty"`  // original filename if available
	DataLen   int         `json:"dataLen,omitempty"`   // size of the data in bytes (we don't store the actual data)
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

// isImageMediaType checks if a MIME type is an image type.
func isImageMediaType(mediaType string) bool {
	return strings.HasPrefix(mediaType, "image/")
}
