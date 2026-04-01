package gittree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LocalAdapter implements TreeProvider using the local filesystem and git CLI.
// It requires the repo to be checked out locally.
type LocalAdapter struct{}

// NewLocalAdapter creates a local git tree adapter.
func NewLocalAdapter() *LocalAdapter {
	return &LocalAdapter{}
}

// Available reports whether the directory exists and is a git repo.
func (a *LocalAdapter) Available(repoPath string) bool {
	info, err := os.Stat(filepath.Join(repoPath, ".git"))
	if err == nil {
		return true
	}
	// Could also be a worktree (.git is a file, not a dir).
	if info != nil && !info.IsDir() {
		return true
	}
	// Try git rev-parse as fallback.
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = repoPath
	return cmd.Run() == nil
}

// ListFiles returns files and directories at the given prefix level.
// It runs `git ls-tree` for one-level listing.
func (a *LocalAdapter) ListFiles(repoPath, ref, prefix string) ([]FileEntry, error) {
	if ref == "" {
		ref = "HEAD"
	}

	// git ls-tree lists entries at one level. The trailing "/" matters.
	treeRef := ref
	if prefix != "" {
		treeRef = ref + ":" + prefix
	}

	out, err := runGit(repoPath, "ls-tree", "--long", treeRef)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}

	entries := make([]FileEntry, 0, len(lines))
	for _, line := range lines {
		e, ok := parseLsTreeLine(line, prefix)
		if ok {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

// LastCommitForFiles returns the last commit for each given file.
// It runs `git log -1` per file (batched in one call per file).
func (a *LocalAdapter) LastCommitForFiles(repoPath, ref string, paths []string) ([]FileCommit, error) {
	if ref == "" {
		ref = "HEAD"
	}
	if len(paths) == 0 {
		return nil, nil
	}

	// Use git log with a custom format for each file.
	// To avoid N separate git calls, we use: git log -1 --format=... -- file1 file2 ...
	// But git log -1 with multiple files returns the SINGLE most recent commit
	// that touched ANY of them, not per-file. So we batch differently.
	//
	// Optimized approach: run a single `git log` per file but cap concurrency.
	// For typical directory listings (< 100 entries), this is fast enough.
	result := make([]FileCommit, 0, len(paths))
	for _, p := range paths {
		fc, err := lastCommitForFile(repoPath, ref, p)
		if err != nil {
			continue // skip files with errors (deleted, etc.)
		}
		result = append(result, fc)
	}
	return result, nil
}

// lastCommitForFile runs `git log -1` for a single file.
func lastCommitForFile(repoPath, ref, filePath string) (FileCommit, error) {
	// Format: SHA<tab>Author<tab>UnixTimestamp<tab>Subject
	format := "%H\t%an\t%at\t%s"
	out, err := runGit(repoPath, "log", "-1", "--format="+format, ref, "--", filePath)
	if err != nil || out == "" {
		return FileCommit{}, err
	}

	parts := strings.SplitN(out, "\t", 4)
	if len(parts) < 4 {
		return FileCommit{}, nil
	}

	ts, _ := strconv.ParseInt(parts[2], 10, 64)
	return FileCommit{
		Path:    filePath,
		SHA:     parts[0],
		Author:  parts[1],
		Date:    time.Unix(ts, 0),
		Message: parts[3],
	}, nil
}

// parseLsTreeLine parses one line of `git ls-tree --long` output.
// Format: <mode> <type> <sha> <size> <name>
// Example: 100644 blob abc123 1234    handlers.go
//
//	040000 tree def456 -       templates
func parseLsTreeLine(line, prefix string) (FileEntry, bool) {
	// Split by whitespace, but size may be "-" for trees and name may have spaces.
	// Format is fixed-width-ish: mode SP type SP sha SP size TAB name
	parts := strings.SplitN(line, "\t", 2)
	if len(parts) != 2 {
		return FileEntry{}, false
	}
	name := parts[1]
	meta := strings.Fields(parts[0]) // mode, type, sha, size
	if len(meta) < 4 {
		return FileEntry{}, false
	}

	isDir := meta[1] == "tree"
	var size int64
	if !isDir {
		size, _ = strconv.ParseInt(meta[3], 10, 64)
	}

	relPath := name
	if prefix != "" {
		relPath = strings.TrimSuffix(prefix, "/") + "/" + name
	}

	return FileEntry{
		Path:  relPath,
		IsDir: isDir,
		Size:  size,
	}, true
}

// runGit executes a git command in the given directory and returns trimmed stdout.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
