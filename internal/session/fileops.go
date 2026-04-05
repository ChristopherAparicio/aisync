package session

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
)

// FileOperation records a single file operation extracted from a tool call.
type FileOperation struct {
	FilePath   string     `json:"file_path"`
	ChangeType ChangeType `json:"change_type"` // created, modified, read, deleted
	ToolName   string     `json:"tool_name"`   // the tool that performed the operation
	MessageIdx int        `json:"message_idx"` // index of the message containing this tool call
}

// ExtractFileOperations walks all tool calls in a session's messages and
// extracts file operations from known tool types. It returns a deduplicated
// list: the strongest operation wins per file path (write > read).
//
// Tool mapping:
//
//	Write / mcp_write        → created (if not seen before) or modified
//	Edit / mcp_edit          → modified
//	Read / mcp_read          → read
//	Glob / mcp_glob          → (skipped — pattern, not a file)
//	Grep / mcp_grep          → (skipped — search, not a file)
//	Bash / mcp_bash          → heuristic: rm → deleted, mkdir/touch → created, etc.
func ExtractFileOperations(messages []Message) []FileOperation {
	// Track the strongest operation per file path.
	// Priority: deleted > created > modified > read
	seen := make(map[string]*FileOperation)

	for msgIdx := range messages {
		msg := &messages[msgIdx]
		for tcIdx := range msg.ToolCalls {
			tc := &msg.ToolCalls[tcIdx]
			ops := extractFromToolCall(tc, msgIdx)
			for _, op := range ops {
				mergeFileOp(seen, &op)
			}
		}
	}

	// Collect results, sorted by file path for determinism.
	result := make([]FileOperation, 0, len(seen))
	for _, op := range seen {
		result = append(result, *op)
	}

	return result
}

// FileOperationsToChanges converts FileOperations to the existing FileChange slice.
func FileOperationsToChanges(ops []FileOperation) []FileChange {
	changes := make([]FileChange, len(ops))
	for i, op := range ops {
		changes[i] = FileChange{
			FilePath:   op.FilePath,
			ChangeType: op.ChangeType,
		}
	}
	return changes
}

// FileBlameResult holds the results of a file blame query.
type FileBlameResult struct {
	FilePath string       `json:"file_path"`
	Entries  []BlameEntry `json:"entries"`
}

// SessionFileStats holds statistics about file operations in a session.
type SessionFileStats struct {
	TotalFiles    int            `json:"total_files"`
	Created       int            `json:"created"`
	Modified      int            `json:"modified"`
	Read          int            `json:"read"`
	Deleted       int            `json:"deleted"`
	ByExtension   map[string]int `json:"by_extension,omitempty"`    // .go → 12, .ts → 5
	ByDirectory   map[string]int `json:"by_directory,omitempty"`    // internal/ → 8, cmd/ → 3
	WriteFiles    []string       `json:"write_files,omitempty"`     // files that were written (created/modified)
	ReadOnlyFiles []string       `json:"read_only_files,omitempty"` // files that were only read
}

// ComputeFileStats computes statistics from a set of file operations.
func ComputeFileStats(ops []FileOperation) SessionFileStats {
	stats := SessionFileStats{
		ByExtension: make(map[string]int),
		ByDirectory: make(map[string]int),
	}

	for _, op := range ops {
		stats.TotalFiles++

		switch op.ChangeType {
		case ChangeCreated:
			stats.Created++
		case ChangeModified:
			stats.Modified++
		case ChangeRead:
			stats.Read++
		case ChangeDeleted:
			stats.Deleted++
		}

		ext := filepath.Ext(op.FilePath)
		if ext == "" {
			ext = "(none)"
		}
		stats.ByExtension[ext]++

		dir := filepath.Dir(op.FilePath)
		if dir == "." || dir == "" {
			dir = "/"
		}
		stats.ByDirectory[dir]++
	}

	// Separate write vs read-only files.
	for _, op := range ops {
		switch op.ChangeType {
		case ChangeCreated, ChangeModified, ChangeDeleted:
			stats.WriteFiles = append(stats.WriteFiles, op.FilePath)
		case ChangeRead:
			stats.ReadOnlyFiles = append(stats.ReadOnlyFiles, op.FilePath)
		}
	}

	return stats
}

// ── internal helpers ──

// extractFromToolCall extracts file operations from a single tool call.
func extractFromToolCall(tc *ToolCall, msgIdx int) []FileOperation {
	name := normalizeToolName(tc.Name)

	switch name {
	case "write":
		return extractWriteOp(tc, msgIdx)
	case "edit":
		return extractEditOp(tc, msgIdx)
	case "read":
		return extractReadOp(tc, msgIdx)
	case "bash":
		return extractBashOps(tc, msgIdx)
	default:
		return nil
	}
}

// normalizeToolName maps tool names to canonical forms.
//
//	"Write", "mcp_write", "mcp_playwright_browser_...", etc.
func normalizeToolName(name string) string {
	lower := strings.ToLower(name)

	// Skip playwright/browser tools — they're not file ops.
	if strings.Contains(lower, "playwright") || strings.Contains(lower, "browser") {
		return ""
	}
	// Skip notion/sentry/langfuse/context7 MCP tools — not file ops.
	if strings.Contains(lower, "notion") || strings.Contains(lower, "sentry") ||
		strings.Contains(lower, "langfuse") || strings.Contains(lower, "context7") {
		return ""
	}

	switch {
	case lower == "write" || lower == "mcp_write":
		return "write"
	case lower == "edit" || lower == "mcp_edit":
		return "edit"
	case lower == "read" || lower == "mcp_read":
		return "read"
	case lower == "bash" || lower == "mcp_bash" || lower == "shell" || lower == "terminal" || lower == "execute_command":
		return "bash"
	default:
		return ""
	}
}

// extractWriteOp extracts file path from Write/mcp_write tool calls.
// Input JSON: {"filePath": "/abs/path", "content": "..."}
func extractWriteOp(tc *ToolCall, msgIdx int) []FileOperation {
	fp := extractFilePath(tc.Input)
	if fp == "" {
		return nil
	}
	return []FileOperation{{
		FilePath:   fp,
		ChangeType: ChangeCreated, // promoted to modified if file seen before
		ToolName:   tc.Name,
		MessageIdx: msgIdx,
	}}
}

// extractEditOp extracts file path from Edit/mcp_edit tool calls.
// Input JSON: {"filePath": "/abs/path", "oldString": "...", "newString": "..."}
func extractEditOp(tc *ToolCall, msgIdx int) []FileOperation {
	fp := extractFilePath(tc.Input)
	if fp == "" {
		return nil
	}
	return []FileOperation{{
		FilePath:   fp,
		ChangeType: ChangeModified,
		ToolName:   tc.Name,
		MessageIdx: msgIdx,
	}}
}

// extractReadOp extracts file path from Read/mcp_read tool calls.
// Input JSON: {"filePath": "/abs/path"} or {"file_path": "/abs/path"}
func extractReadOp(tc *ToolCall, msgIdx int) []FileOperation {
	fp := extractFilePath(tc.Input)
	if fp == "" {
		return nil
	}
	return []FileOperation{{
		FilePath:   fp,
		ChangeType: ChangeRead,
		ToolName:   tc.Name,
		MessageIdx: msgIdx,
	}}
}

// extractBashOps extracts file operations from bash commands using heuristics.
// Detects: rm, mkdir, touch, cp, mv, cat (read), etc.
func extractBashOps(tc *ToolCall, msgIdx int) []FileOperation {
	cmd := extractCommandString(tc.Input)
	if cmd == "" {
		return nil
	}

	var ops []FileOperation

	// Split on && and ; to handle chained commands.
	parts := splitBashCommands(cmd)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Fields(part)
		if len(fields) < 2 {
			continue
		}

		base := baseCommand(fields[0])
		args := nonFlagArgs(fields[1:])

		switch base {
		case "rm":
			for _, a := range args {
				if looksLikeFilePath(a) {
					ops = append(ops, FileOperation{
						FilePath: a, ChangeType: ChangeDeleted,
						ToolName: tc.Name, MessageIdx: msgIdx,
					})
				}
			}
		case "touch":
			for _, a := range args {
				if looksLikeFilePath(a) {
					ops = append(ops, FileOperation{
						FilePath: a, ChangeType: ChangeCreated,
						ToolName: tc.Name, MessageIdx: msgIdx,
					})
				}
			}
		case "cp", "mv":
			// Last arg is destination.
			if len(args) >= 2 {
				dst := args[len(args)-1]
				if looksLikeFilePath(dst) {
					ops = append(ops, FileOperation{
						FilePath: dst, ChangeType: ChangeCreated,
						ToolName: tc.Name, MessageIdx: msgIdx,
					})
				}
			}
		case "cat", "less", "more", "head", "tail":
			for _, a := range args {
				if looksLikeFilePath(a) {
					ops = append(ops, FileOperation{
						FilePath: a, ChangeType: ChangeRead,
						ToolName: tc.Name, MessageIdx: msgIdx,
					})
				}
			}
		case "sed":
			// `sed -i ... file` — in-place edit.
			if containsFlag(fields[1:], "-i") && len(args) > 0 {
				last := args[len(args)-1]
				if looksLikeFilePath(last) {
					ops = append(ops, FileOperation{
						FilePath: last, ChangeType: ChangeModified,
						ToolName: tc.Name, MessageIdx: msgIdx,
					})
				}
			}
		}
	}

	return ops
}

// extractFilePath extracts a file path from tool call JSON input.
// Tries keys: "filePath", "file_path", "path".
func extractFilePath(input string) string {
	input = strings.TrimSpace(input)
	if input == "" || !strings.HasPrefix(input, "{") {
		return ""
	}

	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(input), &obj); err != nil {
		return ""
	}

	// Try multiple key names.
	for _, key := range []string{"filePath", "file_path", "path"} {
		if v, ok := obj[key].(string); ok && v != "" {
			return cleanFilePath(v)
		}
	}
	return ""
}

// cleanFilePath normalises a file path:
//   - trims whitespace
//   - ensures forward slashes
//   - strips trailing slashes (except root)
func cleanFilePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	// Normalise separators.
	p = filepath.ToSlash(p)
	// Strip trailing slash (keep "/" for root).
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	return p
}

// splitBashCommands splits a bash command string on && and ;.
func splitBashCommands(cmd string) []string {
	// Replace && with \x00, then split on ; and \x00.
	cmd = strings.ReplaceAll(cmd, "&&", "\x00")
	var parts []string
	for _, seg := range strings.Split(cmd, "\x00") {
		for _, s := range strings.Split(seg, ";") {
			s = strings.TrimSpace(s)
			if s != "" {
				parts = append(parts, s)
			}
		}
	}
	return parts
}

// baseCommand extracts the command name, stripping path prefix and env vars.
func baseCommand(s string) string {
	// Strip path.
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		s = s[idx+1:]
	}
	return strings.ToLower(s)
}

// nonFlagArgs returns arguments that don't start with -.
func nonFlagArgs(args []string) []string {
	var result []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			result = append(result, a)
		}
	}
	return result
}

// containsFlag checks if a flag is present in args.
func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

// looksLikeFilePath returns true if s looks like a file path (not a flag, not a URL, has extension or slash).
func looksLikeFilePath(s string) bool {
	if s == "" || strings.HasPrefix(s, "-") {
		return false
	}
	// Skip URLs.
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return false
	}
	// Skip common non-path arguments.
	if s == "." || s == ".." || s == "/" {
		return false
	}
	// Must contain a slash or a dot (extension), or start with an abs path.
	return strings.Contains(s, "/") || strings.Contains(s, ".") || strings.HasPrefix(s, "~")
}

// mergeFileOp merges a file operation into the seen map.
// Priority: deleted > created > modified > read.
func mergeFileOp(seen map[string]*FileOperation, op *FileOperation) {
	existing, exists := seen[op.FilePath]
	if !exists {
		clone := *op
		seen[op.FilePath] = &clone
		return
	}

	// Upgrade if the new op is stronger.
	if changePriority(op.ChangeType) > changePriority(existing.ChangeType) {
		existing.ChangeType = op.ChangeType
		existing.ToolName = op.ToolName
		existing.MessageIdx = op.MessageIdx
	}
}

// changePriority returns a numeric priority for merge decisions.
func changePriority(ct ChangeType) int {
	switch ct {
	case ChangeRead:
		return 1
	case ChangeModified:
		return 2
	case ChangeCreated:
		return 3
	case ChangeDeleted:
		return 4
	default:
		return 0
	}
}

// ── Timestamp-enriched file change for storage ──

// SessionFileRecord is a file change enriched with session context for storage.
type SessionFileRecord struct {
	SessionID  ID         `json:"session_id"`
	FilePath   string     `json:"file_path"`
	ChangeType ChangeType `json:"change_type"`
	ToolName   string     `json:"tool_name"`
	CreatedAt  time.Time  `json:"created_at"` // session creation time (for ordering blame entries)
}

// TopFileEntry represents a file with its session touch count for "top files" views.
type TopFileEntry struct {
	FilePath     string `json:"file_path"`
	SessionCount int    `json:"session_count"` // number of distinct sessions that touched this file
	WriteCount   int    `json:"write_count"`   // sessions that created/modified (not just read)
}

// ProjectFileEntry is a file in a project with blame summary for the File Explorer.
type ProjectFileEntry struct {
	FilePath        string       `json:"file_path"`
	SessionCount    int          `json:"session_count"`    // distinct sessions that touched this file
	WriteCount      int          `json:"write_count"`      // sessions that wrote (created/modified/deleted)
	LastChangeType  ChangeType   `json:"last_change_type"` // most recent change type
	LastSessionID   ID           `json:"last_session_id"`  // most recent session that touched this file
	LastSessionTime time.Time    `json:"last_session_time"`
	LastSummary     string       `json:"last_summary"`    // summary of the last session
	LastBranch      string       `json:"last_branch"`     // branch of the last session
	LastProvider    ProviderName `json:"last_provider"`   // provider of the last session
	LastCommitSHA   string       `json:"last_commit_sha"` // commit SHA of the last session
}
