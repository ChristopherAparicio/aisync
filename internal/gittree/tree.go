// Package gittree defines a port for browsing a git repository's file tree.
// The domain interface (TreeProvider) can be backed by a local git checkout,
// a GitHub API adapter, or a cached layer wrapping any provider.
package gittree

import "time"

// FileEntry represents a single file or directory in the git tree.
type FileEntry struct {
	Path    string // relative to repo root (e.g. "internal/web/handlers.go")
	IsDir   bool
	Size    int64  // file size in bytes (0 for dirs)
	LastRef string // last commit SHA that touched this file (empty if unknown)
}

// CommitInfo represents a git commit's metadata.
type CommitInfo struct {
	SHA     string
	Author  string
	Date    time.Time
	Message string // first line only
}

// FileCommit represents the last commit that touched a specific file.
type FileCommit struct {
	Path    string
	SHA     string
	Author  string
	Date    time.Time
	Message string // first line
}

// TreeProvider is the domain port for browsing a git repository's file tree.
// Implementations may use local git, GitHub API, GitLab API, etc.
type TreeProvider interface {
	// ListFiles returns all files under the given directory prefix on the
	// specified ref (branch/tag/SHA). Use ref="" for HEAD and prefix="" for root.
	// The returned entries are files AND directories at the immediate level
	// (not recursive beyond one level).
	ListFiles(repoPath, ref, prefix string) ([]FileEntry, error)

	// LastCommitForFiles returns the last commit info for each of the given
	// file paths on the specified ref. This is the "git log -1" equivalent.
	// Missing files are silently omitted from the result.
	LastCommitForFiles(repoPath, ref string, paths []string) ([]FileCommit, error)

	// Available reports whether this provider can serve the given repo path.
	// For local adapters, this checks if the directory exists and is a git repo.
	// For remote adapters, this checks if credentials are configured.
	Available(repoPath string) bool
}
