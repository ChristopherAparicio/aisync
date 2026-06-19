package session

import (
	"strings"
)

// NormalizeFilePath converts an absolute file path that lives inside projectRoot
// into a path relative to that root, while leaving out-of-project and
// already-relative paths untouched. Storing in-project paths relatively is what
// lets `aisync blame` match the relative paths produced by `git status`, and
// lets sessions captured on one machine be queried on another (teammates, the
// AI5 remote sync branch) where the absolute prefix differs.
//
// Behavior:
//   - in-project absolute -> relative to root ("src/main.go")
//   - out-of-project absolute -> unchanged (still findable, still unambiguous)
//   - already relative -> unchanged (the root prefix never matches)
//   - filePath == root -> "."
//   - projectRoot empty -> filePath unchanged (no anchor, nothing to strip)
//
// It is idempotent: a relative result has no absolute root prefix, so a second
// pass returns it as-is. This matters because capture, import, pull and the
// backfill migration may each run normalization over the same row.
//
// The match is case-insensitive (macOS/Windows filesystems are case-preserving
// but case-insensitive) yet the returned relative path keeps the original
// casing. Separators are normalized to "/" so Windows captures (C:\...) are
// queryable from a POSIX host. We deliberately avoid filepath.IsAbs (it is
// OS-dependent and would mis-classify Windows paths on a POSIX runner) and rely
// on prefix matching instead.
func NormalizeFilePath(filePath, projectRoot string) string {
	// filepath.ToSlash is unusable here: it only swaps the *native* separator,
	// so Windows paths (C:\...) pulled from a teammate onto a POSIX host keep
	// their backslashes. Replace them explicitly to stay host-independent.
	fp := strings.ReplaceAll(strings.TrimSpace(filePath), "\\", "/")
	if fp == "" {
		return ""
	}

	root := strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(projectRoot), "\\", "/"), "/")
	if root == "" {
		return fp
	}

	fpLower := strings.ToLower(fp)
	rootLower := strings.ToLower(root)

	if fpLower == rootLower {
		return "."
	}
	if strings.HasPrefix(fpLower, rootLower+"/") {
		return fp[len(root)+1:]
	}
	return fp
}

// NormalizePathsStats reports the outcome of the file_changes path-normalization
// backfill: how many absolute rows were examined, how many were rewritten to a
// project-relative form, how many stayed absolute (out-of-project), and how many
// could not be normalized because their session had no project_path anchor.
type NormalizePathsStats struct {
	Scanned      int `json:"scanned"`
	Normalized   int `json:"normalized"`
	KeptAbsolute int `json:"kept_absolute"`
	Skipped      int `json:"skipped"`
}

// IsAbsolutePath reports whether p is an absolute path in a host-independent
// way, recognizing both POSIX roots ("/...") and Windows drive roots ("C:\..."
// or "C:/..."). The standard filepath.IsAbs is OS-dependent and would reject
// Windows paths on a POSIX runner, which breaks cross-machine blame lookups.
func IsAbsolutePath(p string) bool {
	p = strings.ReplaceAll(p, "\\", "/")
	if strings.HasPrefix(p, "/") {
		return true
	}
	if len(p) >= 3 && p[1] == ':' && p[2] == '/' &&
		((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) {
		return true
	}
	return false
}

// BlameMatchCandidates expands a user-supplied blame target into every stored
// form it could match, so a lookup succeeds whether file_changes hold the legacy
// absolute path or the normalized relative one. A relative input (e.g. from
// `git status`) yields itself plus the absolute path anchored at projectRoot; an
// in-project absolute input yields its relative form plus itself; an
// out-of-project path (or one with no project root) yields only itself.
func BlameMatchCandidates(input, projectRoot string) []string {
	c := strings.ReplaceAll(strings.TrimSpace(input), "\\", "/")
	if c == "" {
		return nil
	}

	root := strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(projectRoot), "\\", "/"), "/")
	if root == "" {
		return []string{c}
	}

	if IsAbsolutePath(c) {
		rel := NormalizeFilePath(c, root)
		if rel == c {
			return []string{c}
		}
		return []string{rel, c}
	}
	return []string{c, root + "/" + c}
}
