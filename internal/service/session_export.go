package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/converter"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Export ──

// ExportRequest contains inputs for exporting a session.
type ExportRequest struct {
	SessionID   session.ID // empty = use current branch
	ProjectPath string     // used if SessionID is empty
	Branch      string     // used if SessionID is empty
	Format      string     // "aisync", "claude", "opencode", "context"
}

// ExportResult contains the exported data.
type ExportResult struct {
	Data      []byte
	Format    string // normalized format label
	SessionID session.ID
}

// Export converts a session to the requested format.
func (s *SessionService) Export(req ExportRequest) (*ExportResult, error) {
	sess, err := s.resolveSession(req.SessionID, req.ProjectPath, req.Branch)
	if err != nil {
		return nil, err
	}

	var output []byte
	formatLabel := req.Format

	switch req.Format {
	case "aisync", "":
		output, err = json.MarshalIndent(sess, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshaling session: %w", err)
		}
		output = append(output, '\n')
		formatLabel = "aisync"

	case "claude", "claude-code":
		output, err = s.converter.ToNative(sess, session.ProviderClaudeCode)
		if err != nil {
			return nil, fmt.Errorf("converting to Claude format: %w", err)
		}
		formatLabel = "claude"

	case "opencode":
		output, err = s.converter.ToNative(sess, session.ProviderOpenCode)
		if err != nil {
			return nil, fmt.Errorf("converting to OpenCode format: %w", err)
		}

	case "context", "context.md":
		output, err = converter.ToContextMD(sess)
		if err != nil {
			return nil, fmt.Errorf("generating CONTEXT.md: %w", err)
		}
		formatLabel = "context.md"

	default:
		return nil, fmt.Errorf("unknown format %q: valid values are [aisync, claude, opencode, context]", req.Format)
	}

	return &ExportResult{
		Data:      output,
		Format:    formatLabel,
		SessionID: sess.ID,
	}, nil
}

// ── Import ──

// ImportRequest contains inputs for importing a session.
type ImportRequest struct {
	Data         []byte // raw file contents
	SourceFormat string // "aisync", "claude", "opencode", or "" (auto-detect)
	IntoTarget   string // "aisync", "claude-code", "opencode"
}

// ImportResult contains the outcome of an import.
type ImportResult struct {
	SessionID    session.ID
	SourceFormat string
	Target       string
}

// Import parses raw data, optionally scans for secrets, and stores or injects the session.
func (s *SessionService) Import(req ImportRequest) (*ImportResult, error) {
	if len(req.Data) == 0 {
		return nil, fmt.Errorf("import data is empty")
	}

	// Determine source format
	var sourceFormat session.ProviderName
	if req.SourceFormat != "" {
		switch req.SourceFormat {
		case "aisync":
			sourceFormat = "" // unified
		case "claude", "claude-code":
			sourceFormat = session.ProviderClaudeCode
		case "opencode":
			sourceFormat = session.ProviderOpenCode
		default:
			return nil, fmt.Errorf("unknown format %q: valid values are [aisync, claude, opencode]", req.SourceFormat)
		}
	} else {
		sourceFormat = converter.DetectFormat(req.Data)
	}

	// Parse into unified session
	var sess *session.Session
	var err error

	if sourceFormat == "" {
		// Unified aisync JSON
		sess = &session.Session{}
		if jsonErr := json.Unmarshal(req.Data, sess); jsonErr != nil {
			return nil, fmt.Errorf("parsing aisync JSON: %w", jsonErr)
		}
	} else {
		sess, err = s.converter.FromNative(req.Data, sourceFormat)
		if err != nil {
			return nil, fmt.Errorf("parsing %s format: %w", sourceFormat, err)
		}
	}

	// Scan for secrets
	if s.scanner != nil && s.scanner.Mode() == session.SecretModeMask {
		matches := s.scanner.ScanSession(sess)
		if len(matches) > 0 {
			s.scanner.MaskSession(sess)
		}
	}

	// Determine format label for result
	detectedLabel := "aisync"
	if sourceFormat != "" {
		detectedLabel = string(sourceFormat)
	}

	// Determine target
	target := req.IntoTarget
	if target == "" {
		target = "aisync"
	}

	switch target {
	case "aisync":
		if sess.ID == "" {
			sess.ID = session.NewID()
		}
		// Attach owner identity if not already set
		if sess.OwnerID == "" {
			sess.OwnerID = s.resolveOwner()
		}
		s.stampCosts(sess)
		if err := s.store.Save(sess); err != nil {
			return nil, fmt.Errorf("storing session: %w", err)
		}
		s.stampAnalytics(sess)
		// Post-capture hook: extract events, classify errors, fire webhooks, etc.
		// Non-blocking: errors are swallowed (same contract as Capture).
		if s.postCapture != nil {
			s.postCapture(sess)
		}

	case "claude", "claude-code":
		prov, provErr := s.registry.Get(session.ProviderClaudeCode)
		if provErr != nil {
			return nil, fmt.Errorf("claude-code provider: %w", provErr)
		}
		if !prov.CanImport() {
			return nil, fmt.Errorf("claude-code provider does not support import")
		}
		if importErr := prov.Import(sess); importErr != nil {
			return nil, fmt.Errorf("importing into claude-code: %w", importErr)
		}

	case "opencode":
		prov, provErr := s.registry.Get(session.ProviderOpenCode)
		if provErr != nil {
			return nil, fmt.Errorf("opencode provider: %w", provErr)
		}
		if !prov.CanImport() {
			return nil, fmt.Errorf("opencode provider does not support import")
		}
		if importErr := prov.Import(sess); importErr != nil {
			return nil, fmt.Errorf("importing into opencode: %w", importErr)
		}

	default:
		return nil, fmt.Errorf("unknown target %q: valid values are [aisync, claude-code, opencode]", target)
	}

	return &ImportResult{
		SessionID:    sess.ID,
		SourceFormat: detectedLabel,
		Target:       target,
	}, nil
}

// ── Bulk export (JSONL bundle) ──

// ExportAllRequest selects which sessions to include in a bulk export.
// The output is always the unified aisync format, serialized as JSONL
// (one compact session JSON object per line) so it streams and concatenates
// cleanly and transfers easily between two aisync servers.
type ExportAllRequest struct {
	ProjectPath string
	Branch      string
	Provider    session.ProviderName
	All         bool // ignore Branch: every session in the project (all branches)
	Global      bool // ignore ProjectPath+Branch: every session across all projects
}

// ExportAllResult contains the JSONL bundle and how many sessions it holds.
type ExportAllResult struct {
	Data  []byte
	Count int
}

// ExportAll serializes every session matching req into a JSONL bundle. Each
// line is a complete unified aisync session object. The bundle round-trips
// through ImportBundle.
func (s *SessionService) ExportAll(req ExportAllRequest) (*ExportAllResult, error) {
	summaries, err := s.List(ListRequest{
		ProjectPath: req.ProjectPath,
		Branch:      req.Branch,
		Provider:    req.Provider,
		All:         req.All,
		Global:      req.Global,
	})
	if err != nil {
		return nil, err
	}
	if len(summaries) == 0 {
		return &ExportAllResult{}, nil
	}

	ids := make([]session.ID, 0, len(summaries))
	for _, sm := range summaries {
		ids = append(ids, sm.ID)
	}

	batch, err := s.store.GetBatch(ids)
	if err != nil {
		return nil, fmt.Errorf("loading sessions: %w", err)
	}

	var buf bytes.Buffer
	count := 0
	for _, id := range ids {
		sess, ok := batch[id]
		if !ok || sess == nil {
			continue
		}
		line, marshalErr := json.Marshal(sess)
		if marshalErr != nil {
			return nil, fmt.Errorf("marshaling session %s: %w", id, marshalErr)
		}
		buf.Write(line)
		buf.WriteByte('\n')
		count++
	}

	return &ExportAllResult{Data: buf.Bytes(), Count: count}, nil
}

// ── Bulk import (JSONL bundle) ──

// IsBundle reports whether data is a multi-session JSONL bundle produced by
// ExportAll: two or more non-empty lines, each a standalone unified aisync
// session object (carrying both "provider" and "messages"). A pretty-printed
// single export (whose first line is just "{") and Claude JSONL (lines carry
// "type", not the session shape) are NOT bundles. The two-line minimum lets a
// single compact session JSON fall through to the normal single-session import
// path, which stores the identical result while preserving direct error
// reporting (e.g. an unknown --into target).
func IsBundle(data []byte) bool {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return false
	}
	nonEmpty := 0
	for _, ln := range strings.Split(trimmed, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(ln), &obj); err != nil {
			return false
		}
		if _, ok := obj["provider"]; !ok {
			return false
		}
		if _, ok := obj["messages"]; !ok {
			return false
		}
		nonEmpty++
	}
	return nonEmpty >= 2
}

// ImportBundleResult summarizes a bulk import.
type ImportBundleResult struct {
	SessionIDs []session.ID
	Errors     []string
	Imported   int
	Failed     int
}

// ImportBundle imports every session line of a JSONL bundle. Each line is
// routed through the standard Import path (secret scan, owner stamping,
// post-capture hook), so a bundle import is equivalent to importing each
// session individually. A failed line is recorded and does not abort the rest.
func (s *SessionService) ImportBundle(req ImportRequest) (*ImportBundleResult, error) {
	trimmed := strings.TrimSpace(string(req.Data))
	if trimmed == "" {
		return nil, fmt.Errorf("import data is empty")
	}

	result := &ImportBundleResult{}
	for i, ln := range strings.Split(trimmed, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		res, err := s.Import(ImportRequest{
			Data:         []byte(ln),
			SourceFormat: "aisync",
			IntoTarget:   req.IntoTarget,
		})
		if err != nil {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("line %d: %v", i+1, err))
			continue
		}
		result.Imported++
		result.SessionIDs = append(result.SessionIDs, res.SessionID)
	}
	return result, nil
}
