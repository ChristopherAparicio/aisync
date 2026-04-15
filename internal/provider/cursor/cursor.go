// Package cursor implements the Cursor provider for aisync.
// It reads sessions from Cursor's SQLite-based storage (state.vscdb).
//
// Cursor stores conversation metadata in two locations:
//   - Workspace state.vscdb (ItemTable, key "composer.composerData"): list of
//     composers with IDs, branches, timestamps, and mode info.
//   - Global state.vscdb (cursorDiskKV, keys "composerData:<uuid>"): full
//     composer data including conversation headers, token usage, file changes,
//     model config, context, code block edits, and usage cost data.
//
// Schema versions tracked:
//   - _v 13-14 (Cursor 2025-2026): adds usageData (costInCents), codeBlockData,
//     todos, isAgentic/isNAL flags, newlyCreatedFiles, subagentComposerIds,
//     conversationState (encrypted message content).
//
// This provider is export-only: CanImport() returns false.
package cursor

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"

	_ "modernc.org/sqlite" // SQLite driver registration
)

const (
	// globalStatePath is the path to the global state.vscdb relative to cursor user dir.
	globalStatePath = "globalStorage/state.vscdb"

	// workspaceStorageDir is the path to workspace storage relative to cursor user dir.
	workspaceStorageDir = "workspaceStorage"

	// Keys in the ItemTable / cursorDiskKV.
	composerDataKey    = "composer.composerData"
	composerDataPrefix = "composerData:"

	// Bubble types.
	bubbleTypeUser      = 1
	bubbleTypeAssistant = 2

	defaultAgent    = "cursor-agent"
	exportedByLabel = "aisync"
)

// Provider implements session.Provider for Cursor (export only).
type Provider struct {
	// cursorUserDir overrides the default Cursor User directory (for testing).
	cursorUserDir string
}

// New creates a Cursor provider.
// If cursorUserDir is empty, it defaults to the platform-specific location.
func New(cursorUserDir string) *Provider {
	if cursorUserDir == "" {
		cursorUserDir = defaultCursorUserDir()
	}
	return &Provider{cursorUserDir: cursorUserDir}
}

// Name returns the provider identifier.
func (p *Provider) Name() session.ProviderName {
	return session.ProviderCursor
}

// CanImport reports that Cursor does not support session import.
func (p *Provider) CanImport() bool {
	return false
}

// Import returns ErrImportNotSupported since Cursor is export-only.
func (p *Provider) Import(_ *session.Session) error {
	return session.ErrImportNotSupported
}

// Detect finds sessions matching the given project and branch.
// It reads workspace metadata from the workspace state.vscdb, then enriches
// with data from the global state.vscdb.
func (p *Provider) Detect(projectPath string, branch string) ([]session.Summary, error) {
	// Find the workspace hash for this project
	wsHash, findErr := p.findWorkspaceHash(projectPath)
	if findErr != nil {
		return nil, findErr
	}

	// Read the workspace's composer list
	wsDBPath := filepath.Join(p.cursorUserDir, workspaceStorageDir, wsHash, "state.vscdb")
	composers, readErr := readWorkspaceComposers(wsDBPath)
	if readErr != nil {
		return nil, fmt.Errorf("reading workspace composers: %w", readErr)
	}

	// Filter by branch if specified
	var matches []composerHead
	for _, c := range composers {
		if branch != "" && !c.matchesBranch(branch) {
			continue
		}
		matches = append(matches, c)
	}

	// Convert to session summaries
	summaries := make([]session.Summary, 0, len(matches))
	for _, c := range matches {
		summaries = append(summaries, session.Summary{
			ID:           session.ID(c.ComposerID),
			Provider:     session.ProviderCursor,
			Agent:        agentFromMode(c.UnifiedMode),
			Branch:       c.activeBranch(),
			Summary:      c.Name,
			MessageCount: 0, // not available from workspace metadata
			CreatedAt:    c.createdTime(),
		})
	}

	// Sort by created_at descending (most recent first)
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].CreatedAt.After(summaries[j].CreatedAt)
	})

	return summaries, nil
}

// Export reads a session by ID and converts it to the unified format.
func (p *Provider) Export(sessionID session.ID, mode session.StorageMode) (*session.Session, error) {
	// Read full composer data from the global state.vscdb
	globalDBPath := filepath.Join(p.cursorUserDir, globalStatePath)
	cd, readErr := readComposerData(globalDBPath, string(sessionID))
	if readErr != nil {
		return nil, fmt.Errorf("reading composer data: %w", readErr)
	}

	sess := &session.Session{
		ID:              sessionID,
		Version:         1,
		Provider:        session.ProviderCursor,
		Agent:           cd.resolveAgent(),
		Branch:          cd.activeBranch(),
		ProjectPath:     "", // filled by caller if needed
		ExportedBy:      exportedByLabel,
		ExportedAt:      time.Now(),
		CreatedAt:       cd.createdTime(),
		Summary:         cd.enrichedSummary(),
		StorageMode:     mode,
		SourceUpdatedAt: cd.LastUpdatedAt,
		TokenUsage:      cd.buildTokenUsage(),
	}

	// Extract actual cost from usageData (available since schema _v 13+)
	sess.ActualCost = cd.totalCostUSD()

	// Extract messages from conversation headers
	if mode != session.StorageModeSummary {
		sess.Messages = cd.extractMessages()
	}

	// Extract file changes — prefer structured data over subtitle parsing
	sess.FileChanges = cd.extractFileChanges()

	// Export sub-agent sessions as children (available since schema _v 13+)
	if len(cd.SubagentComposerIds) > 0 {
		sess.Children = p.exportChildren(cd.SubagentComposerIds, mode)
	}

	return sess, nil
}

// exportChildren loads sub-agent sessions by their composer IDs.
// Failures for individual children are silently skipped — partial results are acceptable.
func (p *Provider) exportChildren(subagentIDs []string, mode session.StorageMode) []session.Session {
	globalDBPath := filepath.Join(p.cursorUserDir, globalStatePath)
	var children []session.Session

	for _, subID := range subagentIDs {
		cd, err := readComposerData(globalDBPath, subID)
		if err != nil {
			continue // sub-agent data not found or corrupt — skip
		}

		child := session.Session{
			ID:              session.ID(subID),
			Version:         1,
			Provider:        session.ProviderCursor,
			Agent:           cd.resolveAgent(),
			Branch:          cd.activeBranch(),
			ExportedBy:      exportedByLabel,
			ExportedAt:      time.Now(),
			CreatedAt:       cd.createdTime(),
			Summary:         cd.enrichedSummary(),
			StorageMode:     mode,
			SourceUpdatedAt: cd.LastUpdatedAt,
			TokenUsage:      cd.buildTokenUsage(),
			ActualCost:      cd.totalCostUSD(),
			FileChanges:     cd.extractFileChanges(),
		}

		if mode != session.StorageModeSummary {
			child.Messages = cd.extractMessages()
		}

		// Note: we do NOT recurse into sub-sub-agents to avoid deep nesting.
		// Cursor sub-agents are typically leaf nodes.

		children = append(children, child)
	}

	return children
}

// --- Internal types ---

// composerHead is the lightweight metadata from the workspace's ItemTable.
type composerHead struct {
	ComposerID        string `json:"composerId"`
	Name              string `json:"name"`
	UnifiedMode       string `json:"unifiedMode"`
	Subtitle          string `json:"subtitle"`
	CommittedToBranch string `json:"committedToBranch"`
	CreatedOnBranch   string `json:"createdOnBranch"`
	LastUpdatedAt     int64  `json:"lastUpdatedAt"`
	CreatedAt         int64  `json:"createdAt"`
	FilesChangedCount int    `json:"filesChangedCount"`
	TotalLinesAdded   int    `json:"totalLinesAdded"`
	TotalLinesRemoved int    `json:"totalLinesRemoved"`
}

func (c *composerHead) createdTime() time.Time {
	if c.CreatedAt > 0 {
		return time.UnixMilli(c.CreatedAt)
	}
	return time.Time{}
}

func (c *composerHead) activeBranch() string {
	if c.CommittedToBranch != "" {
		return c.CommittedToBranch
	}
	return c.CreatedOnBranch
}

func (c *composerHead) matchesBranch(branch string) bool {
	return c.CommittedToBranch == branch || c.CreatedOnBranch == branch
}

// workspaceComposers is the top-level structure in composer.composerData.
type workspaceComposers struct {
	AllComposers []composerHead `json:"allComposers"`
}

// composerData is the full data from the global cursorDiskKV.
// This struct covers Cursor schema versions _v 10-14.
type composerData struct {
	// Core fields (all versions)
	ModelConfig                 *modelConfig `json:"modelConfig,omitempty"`
	CommittedToBranch           string       `json:"committedToBranch"`
	Name                        string       `json:"name"`
	CreatedOnBranch             string       `json:"createdOnBranch"`
	Subtitle                    string       `json:"subtitle"`
	UnifiedMode                 string       `json:"unifiedMode"`
	ComposerID                  string       `json:"composerId"`
	Status                      string       `json:"status"`
	FullConversationHeadersOnly []bubble     `json:"fullConversationHeadersOnly"`
	FilesChangedCount           int          `json:"filesChangedCount"`
	CreatedAt                   int64        `json:"createdAt"`
	LastUpdatedAt               int64        `json:"lastUpdatedAt"`
	TotalLinesRemoved           int          `json:"totalLinesRemoved"`
	ContextTokensUsed           int          `json:"contextTokensUsed"`
	TotalLinesAdded             int          `json:"totalLinesAdded"`
	ContextTokenLimit           int          `json:"contextTokenLimit"`

	// New in _v 13+ — cost tracking
	UsageData map[string]*usageEntry `json:"usageData,omitempty"`

	// New in _v 13+ — code edits per file (tool call reconstruction)
	// Outer key: file URI, inner key: codeblock ID → block details
	CodeBlockData map[string]map[string]*codeBlock `json:"codeBlockData,omitempty"`

	// New in _v 13+ — structured file tracking
	NewlyCreatedFiles []fileRef `json:"newlyCreatedFiles,omitempty"`
	// originalFileStates maps file URI → pre-edit content/state (not parsed, just counted)
	OriginalFileStates map[string]json.RawMessage `json:"originalFileStates,omitempty"`

	// New in _v 13+ — agent capabilities
	IsAgentic bool   `json:"isAgentic,omitempty"`
	IsNAL     bool   `json:"isNAL,omitempty"` // Next Action Loop
	ForceMode string `json:"forceMode,omitempty"`

	// New in _v 13+ — structured task tracking
	Todos []todoItem `json:"todos,omitempty"`

	// New in _v 13+ — sub-agent sessions
	SubagentComposerIds []string `json:"subagentComposerIds,omitempty"`

	// New in _v 13+ — context saturation
	ContextUsagePercent float64 `json:"contextUsagePercent,omitempty"`

	// Schema version
	SchemaVersion int `json:"_v,omitempty"`
}

// usageEntry tracks per-model cost data within a session.
type usageEntry struct {
	CostInCents int `json:"costInCents"` // total cost in cents (e.g. 245 = $2.45)
	Amount      int `json:"amount"`      // number of API requests
}

// codeBlock represents a single file edit operation tracked by Cursor.
type codeBlock struct {
	BubbleID    string  `json:"bubbleId"`    // links to a message/bubble
	CodeBlockID string  `json:"codeblockId"` // unique ID for this edit
	Status      string  `json:"status"`      // "completed", "accepted", "aborted"
	LanguageID  string  `json:"languageId"`  // file language (e.g. "python", "go")
	CreatedAt   int64   `json:"createdAt"`   // epoch ms
	URI         *uriRef `json:"uri,omitempty"`
}

// uriRef is the VS Code URI reference used in codeBlockData and newlyCreatedFiles.
type uriRef struct {
	FSPath   string `json:"fsPath"`
	External string `json:"external"`
	Path     string `json:"path"`
	Scheme   string `json:"scheme"`
}

// fileRef is a file reference with a nested URI, used in newlyCreatedFiles.
type fileRef struct {
	URI *uriRef `json:"uri,omitempty"`
}

// todoItem is a structured task from Cursor's todo/plan system.
type todoItem struct {
	ID           string   `json:"id"`
	Content      string   `json:"content"`
	Status       string   `json:"status"` // "completed", "pending", "in_progress"
	Dependencies []string `json:"dependencies,omitempty"`
}

type bubble struct {
	BubbleID string `json:"bubbleId"`
	Type     int    `json:"type"`
}

type modelConfig struct {
	ModelName string `json:"modelName"`
	MaxMode   bool   `json:"maxMode,omitempty"`
}

func (cd *composerData) createdTime() time.Time {
	if cd.CreatedAt > 0 {
		return time.UnixMilli(cd.CreatedAt)
	}
	return time.Time{}
}

func (cd *composerData) activeBranch() string {
	if cd.CommittedToBranch != "" {
		return cd.CommittedToBranch
	}
	return cd.CreatedOnBranch
}

// buildTokenUsage maps Cursor's context tracking fields to the unified TokenUsage.
// Cursor tracks contextTokensUsed (cumulative input) and contextTokenLimit (model window).
// We map contextTokensUsed to InputTokens (cumulative) and TotalTokens.
func (cd *composerData) buildTokenUsage() session.TokenUsage {
	tu := session.TokenUsage{
		TotalTokens: cd.ContextTokensUsed,
	}
	// InputTokens = cumulative context tokens used (Cursor tracks cumulative, not per-message)
	if cd.ContextTokensUsed > 0 {
		tu.InputTokens = cd.ContextTokensUsed
	}
	return tu
}

// enrichedSummary returns the session name enriched with todo progress when available.
// Example: "Implement OAuth2 [3/5 tasks]" when todos are present.
func (cd *composerData) enrichedSummary() string {
	if len(cd.Todos) == 0 {
		return cd.Name
	}

	completed := 0
	for _, t := range cd.Todos {
		if t.Status == "completed" {
			completed++
		}
	}

	return fmt.Sprintf("%s [%d/%d tasks]", cd.Name, completed, len(cd.Todos))
}

// resolveAgent returns the agent name, using richer signals when available.
func (cd *composerData) resolveAgent() string {
	if cd.IsAgentic || cd.IsNAL {
		return "cursor-agent"
	}
	return agentFromMode(cd.UnifiedMode)
}

// totalCostUSD returns the total session cost in USD from usageData.
// Returns 0 if no cost data is available (older Cursor versions).
func (cd *composerData) totalCostUSD() float64 {
	if len(cd.UsageData) == 0 {
		return 0
	}
	var totalCents int
	for _, entry := range cd.UsageData {
		if entry != nil {
			totalCents += entry.CostInCents
		}
	}
	return float64(totalCents) / 100.0
}

// totalAPIRequests returns the total number of API requests from usageData.
func (cd *composerData) totalAPIRequests() int {
	if len(cd.UsageData) == 0 {
		return 0
	}
	var total int
	for _, entry := range cd.UsageData {
		if entry != nil {
			total += entry.Amount
		}
	}
	return total
}

// extractMessages builds session messages from conversation headers and enriches
// them with tool calls reconstructed from codeBlockData.
func (cd *composerData) extractMessages() []session.Message {
	messages := make([]session.Message, 0, len(cd.FullConversationHeadersOnly))

	// Pre-build bubble → tool calls index from codeBlockData
	bubbleToolCalls := cd.buildToolCallIndex()

	// Distribute cost evenly across API requests if we have usageData
	costPerRequest := 0.0
	apiRequests := cd.totalAPIRequests()
	if apiRequests > 0 && cd.totalCostUSD() > 0 {
		costPerRequest = cd.totalCostUSD() / float64(apiRequests)
	}

	assistantCount := 0
	for _, b := range cd.FullConversationHeadersOnly {
		role := session.RoleUser
		if b.Type == bubbleTypeAssistant {
			role = session.RoleAssistant
		}

		msg := session.Message{
			ID:   b.BubbleID,
			Role: role,
			// Cursor stores message text in encrypted conversationState;
			// without decryption, content remains empty.
			Content: "",
		}

		if role == session.RoleAssistant {
			if cd.ModelConfig != nil {
				msg.Model = cd.ModelConfig.ModelName
			}

			// Attach reconstructed tool calls for this bubble
			if tcs, ok := bubbleToolCalls[b.BubbleID]; ok {
				msg.ToolCalls = tcs
			}

			// Distribute cost across assistant messages (approximation)
			assistantCount++
			if costPerRequest > 0 {
				msg.ProviderCost = costPerRequest
			}
		}

		messages = append(messages, msg)
	}

	return messages
}

// buildToolCallIndex groups codeBlockData entries by bubbleId,
// creating session.ToolCall slices for each assistant message.
func (cd *composerData) buildToolCallIndex() map[string][]session.ToolCall {
	if len(cd.CodeBlockData) == 0 {
		return nil
	}

	index := make(map[string][]session.ToolCall)

	for fileURI, blocks := range cd.CodeBlockData {
		for _, block := range blocks {
			if block == nil || block.BubbleID == "" {
				continue
			}

			// Determine the file path (prefer fsPath from URI)
			filePath := fileURI
			if block.URI != nil && block.URI.FSPath != "" {
				filePath = block.URI.FSPath
			}

			// Map Cursor status to ToolState
			state := mapCodeBlockStatus(block.Status)

			tc := session.ToolCall{
				ID:    block.CodeBlockID,
				Name:  "edit", // Cursor code blocks are file edits
				Input: filePath,
				State: state,
			}

			if block.CreatedAt > 0 {
				// DurationMs not available from codeBlockData, but we store CreatedAt
				// for ordering/timeline purposes.
			}

			index[block.BubbleID] = append(index[block.BubbleID], tc)
		}
	}

	// Sort tool calls within each bubble by CodeBlockID for deterministic output
	for bubbleID, tcs := range index {
		sort.Slice(tcs, func(i, j int) bool {
			return tcs[i].ID < tcs[j].ID
		})
		index[bubbleID] = tcs
	}

	return index
}

// mapCodeBlockStatus maps Cursor's code block status to session.ToolState.
func mapCodeBlockStatus(status string) session.ToolState {
	switch status {
	case "completed", "accepted":
		return session.ToolStateCompleted
	case "aborted":
		return session.ToolStateError
	default:
		return session.ToolStatePending
	}
}

// extractFileChanges builds a comprehensive file change list from multiple sources.
// Priority: newlyCreatedFiles + originalFileStates > subtitle parsing.
func (cd *composerData) extractFileChanges() []session.FileChange {
	// Collect unique files from structured data
	seen := make(map[string]session.ChangeType)

	// 1. Files from newlyCreatedFiles → Created
	for _, f := range cd.NewlyCreatedFiles {
		if f.URI != nil && f.URI.FSPath != "" {
			seen[f.URI.FSPath] = session.ChangeCreated
		}
	}

	// 2. Files from originalFileStates → Modified (had pre-edit state)
	for fileURI := range cd.OriginalFileStates {
		path := fileURI
		// Strip file:// prefix if present
		path = strings.TrimPrefix(path, "file://")
		if decoded, err := url.PathUnescape(path); err == nil {
			path = decoded
		}
		if _, exists := seen[path]; !exists {
			seen[path] = session.ChangeModified
		}
	}

	// 3. Files from codeBlockData URIs → Modified (if not already tracked)
	for _, blocks := range cd.CodeBlockData {
		for _, block := range blocks {
			if block != nil && block.URI != nil && block.URI.FSPath != "" {
				if _, exists := seen[block.URI.FSPath]; !exists {
					seen[block.URI.FSPath] = session.ChangeModified
				}
			}
		}
	}

	// If no structured data, fall back to subtitle parsing
	if len(seen) == 0 && cd.FilesChangedCount > 0 && cd.Subtitle != "" {
		return extractFileChangesFromSubtitle(cd.Subtitle)
	}

	// Convert map to sorted slice
	changes := make([]session.FileChange, 0, len(seen))
	for path, changeType := range seen {
		changes = append(changes, session.FileChange{
			FilePath:   path,
			ChangeType: changeType,
		})
	}
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].FilePath < changes[j].FilePath
	})

	return changes
}

func (b *bubble) bubbleID() string {
	return b.BubbleID
}

// --- Database helpers ---

// findWorkspaceHash finds the workspace storage hash for a given project path.
func (p *Provider) findWorkspaceHash(projectPath string) (string, error) {
	wsDir := filepath.Join(p.cursorUserDir, workspaceStorageDir)
	entries, readErr := os.ReadDir(wsDir)
	if readErr != nil {
		return "", fmt.Errorf("reading workspace storage: %w", readErr)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		wsJSON := filepath.Join(wsDir, entry.Name(), "workspace.json")
		data, fileErr := os.ReadFile(wsJSON)
		if fileErr != nil {
			continue
		}

		var ws workspaceJSON
		if jsonErr := json.Unmarshal(data, &ws); jsonErr != nil {
			continue
		}

		wsPath := ws.extractPath()
		if wsPath == projectPath || normalizePath(wsPath) == normalizePath(projectPath) {
			return entry.Name(), nil
		}
	}

	return "", fmt.Errorf("no Cursor workspace found for %s: %w", projectPath, session.ErrProviderNotDetected)
}

type workspaceJSON struct {
	Folder    string `json:"folder"`
	Workspace string `json:"workspace"`
}

func (w *workspaceJSON) extractPath() string {
	raw := w.Folder
	if raw == "" {
		raw = w.Workspace
	}
	if raw == "" {
		return ""
	}

	// Remove file:// prefix and decode URL encoding
	raw = strings.TrimPrefix(raw, "file://")
	decoded, decodeErr := url.PathUnescape(raw)
	if decodeErr != nil {
		return raw
	}
	return decoded
}

// readWorkspaceComposers reads the composer list from a workspace's state.vscdb.
func readWorkspaceComposers(dbPath string) ([]composerHead, error) {
	db, openErr := sql.Open("sqlite", dbPath+"?mode=ro")
	if openErr != nil {
		return nil, openErr
	}
	defer func() { _ = db.Close() }()

	var raw []byte
	queryErr := db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", composerDataKey).Scan(&raw)
	if queryErr != nil {
		return nil, fmt.Errorf("reading composer data: %w", queryErr)
	}

	var wc workspaceComposers
	if jsonErr := json.Unmarshal(raw, &wc); jsonErr != nil {
		return nil, fmt.Errorf("parsing composer data: %w", jsonErr)
	}

	return wc.AllComposers, nil
}

// readComposerData reads a single composer's full data from the global state.vscdb.
func readComposerData(dbPath string, composerID string) (*composerData, error) {
	db, openErr := sql.Open("sqlite", dbPath+"?mode=ro")
	if openErr != nil {
		return nil, openErr
	}
	defer func() { _ = db.Close() }()

	key := composerDataPrefix + composerID
	var raw []byte
	queryErr := db.QueryRow("SELECT value FROM cursorDiskKV WHERE key = ?", key).Scan(&raw)
	if queryErr != nil {
		return nil, fmt.Errorf("reading composer %s: %w", composerID, queryErr)
	}

	var cd composerData
	if jsonErr := json.Unmarshal(raw, &cd); jsonErr != nil {
		return nil, fmt.Errorf("parsing composer %s: %w", composerID, jsonErr)
	}

	return &cd, nil
}

// --- Helpers ---

// defaultCursorUserDir returns the platform-specific Cursor user data directory.
func defaultCursorUserDir() string {
	switch runtime.GOOS {
	case "darwin":
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return ""
		}
		return filepath.Join(home, "Library", "Application Support", "Cursor", "User")
	case "linux":
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return ""
		}
		return filepath.Join(home, ".config", "Cursor", "User")
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return ""
		}
		return filepath.Join(appData, "Cursor", "User")
	default:
		return ""
	}
}

// agentFromMode maps Cursor's unifiedMode to an agent name.
func agentFromMode(mode string) string {
	switch mode {
	case "agent":
		return "cursor-agent"
	case "chat":
		return "cursor-chat"
	default:
		return defaultAgent
	}
}

// normalizePath normalizes a file path for comparison.
func normalizePath(p string) string {
	return filepath.Clean(p)
}

// extractFileChangesFromSubtitle parses Cursor's subtitle field which lists changed files.
// The format is: "Edited file1.go, file2.go, file3.go"
// This is the legacy fallback when structured file data is not available.
func extractFileChangesFromSubtitle(subtitle string) []session.FileChange {
	subtitle = strings.TrimPrefix(subtitle, "Edited ")
	subtitle = strings.TrimPrefix(subtitle, "Created ")
	subtitle = strings.TrimPrefix(subtitle, "Deleted ")

	parts := strings.Split(subtitle, ", ")
	changes := make([]session.FileChange, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		changes = append(changes, session.FileChange{
			FilePath:   part,
			ChangeType: session.ChangeModified,
		})
	}
	return changes
}

// extractFileChanges is kept as a package-level alias for backward compatibility with tests.
func extractFileChanges(subtitle string) []session.FileChange {
	return extractFileChangesFromSubtitle(subtitle)
}
