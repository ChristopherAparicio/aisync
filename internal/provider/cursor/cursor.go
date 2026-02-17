// Package cursor implements the Cursor provider for aisync.
// It reads sessions from Cursor's SQLite-based storage (state.vscdb).
//
// Cursor stores conversation metadata in two locations:
//   - Workspace state.vscdb (ItemTable, key "composer.composerData"): list of
//     composers with IDs, branches, timestamps, and mode info.
//   - Global state.vscdb (cursorDiskKV, keys "composerData:<uuid>"): full
//     composer data including conversation headers, token usage, file changes,
//     model config, and context.
//
// Note: Cursor stores actual message text server-side. Locally, only bubble IDs
// and types (1=user, 2=assistant) are available. The provider extracts all
// available metadata to produce useful session summaries.
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

	"github.com/ChristopherAparicio/aisync/internal/domain"

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

// Provider implements domain.Provider for Cursor (export only).
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
func (p *Provider) Name() domain.ProviderName {
	return domain.ProviderCursor
}

// CanImport reports that Cursor does not support session import.
func (p *Provider) CanImport() bool {
	return false
}

// Import returns ErrImportNotSupported since Cursor is export-only.
func (p *Provider) Import(_ *domain.Session) error {
	return domain.ErrImportNotSupported
}

// Detect finds sessions matching the given project and branch.
// It reads workspace metadata from the workspace state.vscdb, then enriches
// with data from the global state.vscdb.
func (p *Provider) Detect(projectPath string, branch string) ([]domain.SessionSummary, error) {
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
	summaries := make([]domain.SessionSummary, 0, len(matches))
	for _, c := range matches {
		summaries = append(summaries, domain.SessionSummary{
			ID:           domain.SessionID(c.ComposerID),
			Provider:     domain.ProviderCursor,
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
func (p *Provider) Export(sessionID domain.SessionID, mode domain.StorageMode) (*domain.Session, error) {
	// Read full composer data from the global state.vscdb
	globalDBPath := filepath.Join(p.cursorUserDir, globalStatePath)
	cd, readErr := readComposerData(globalDBPath, string(sessionID))
	if readErr != nil {
		return nil, fmt.Errorf("reading composer data: %w", readErr)
	}

	session := &domain.Session{
		ID:          sessionID,
		Version:     1,
		Provider:    domain.ProviderCursor,
		Agent:       agentFromMode(cd.UnifiedMode),
		Branch:      cd.activeBranch(),
		ProjectPath: "", // filled by caller if needed
		ExportedBy:  exportedByLabel,
		ExportedAt:  time.Now(),
		CreatedAt:   cd.createdTime(),
		Summary:     cd.Name,
		StorageMode: mode,
		TokenUsage: domain.TokenUsage{
			TotalTokens: cd.ContextTokensUsed,
		},
	}

	// Extract messages from conversation headers
	if mode != domain.StorageModeSummary {
		session.Messages = cd.extractMessages()
	}

	// Extract file changes from subtitle
	if cd.FilesChangedCount > 0 && cd.Subtitle != "" {
		session.FileChanges = extractFileChanges(cd.Subtitle)
	}

	return session, nil
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
type composerData struct {
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
}

type bubble struct {
	BubbleID string `json:"bubbleId"`
	Type     int    `json:"type"`
}

type modelConfig struct {
	ModelName string `json:"modelName"`
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

func (cd *composerData) extractMessages() []domain.Message {
	messages := make([]domain.Message, 0, len(cd.FullConversationHeadersOnly))
	for _, b := range cd.FullConversationHeadersOnly {
		role := domain.RoleUser
		if b.Type == bubbleTypeAssistant {
			role = domain.RoleAssistant
		}

		msg := domain.Message{
			ID:   b.bubbleID(),
			Role: role,
			// Cursor stores message text server-side; only IDs are available locally.
			Content: "",
		}

		if role == domain.RoleAssistant && cd.ModelConfig != nil {
			msg.Model = cd.ModelConfig.ModelName
		}

		messages = append(messages, msg)
	}
	return messages
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

	return "", fmt.Errorf("no Cursor workspace found for %s: %w", projectPath, domain.ErrProviderNotDetected)
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

// extractFileChanges parses Cursor's subtitle field which lists changed files.
// The format is: "Edited file1.go, file2.go, file3.go"
func extractFileChanges(subtitle string) []domain.FileChange {
	subtitle = strings.TrimPrefix(subtitle, "Edited ")
	subtitle = strings.TrimPrefix(subtitle, "Created ")
	subtitle = strings.TrimPrefix(subtitle, "Deleted ")

	parts := strings.Split(subtitle, ", ")
	changes := make([]domain.FileChange, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		changes = append(changes, domain.FileChange{
			FilePath:   part,
			ChangeType: domain.ChangeModified,
		})
	}
	return changes
}
