package replay

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Worktree manages temporary git worktrees for isolated replay execution.
type Worktree struct {
	repoDir      string // original git repo directory
	worktreePath string // path to the created worktree
}

// CreateWorktree creates a temporary git worktree at the given commit.
// If commitSHA is empty, HEAD is used.
// Returns the Worktree handle (call Remove() when done).
func CreateWorktree(repoDir, commitSHA string) (*Worktree, error) {
	// Verify it's a git repo.
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
		return nil, fmt.Errorf("not a git repo: %s", repoDir)
	}

	// Create a temporary directory for the worktree.
	tmpDir, err := os.MkdirTemp("", "aisync-replay-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}

	// Determine the ref to checkout.
	ref := commitSHA
	if ref == "" {
		ref = "HEAD"
	}

	// Create the worktree.
	// git worktree add --detach <path> <ref>
	cmd := exec.Command("git", "worktree", "add", "--detach", tmpDir, ref)
	cmd.Dir = repoDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		// Cleanup the temp dir on failure.
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("git worktree add: %w (%s)", err, strings.TrimSpace(string(output)))
	}

	return &Worktree{
		repoDir:      repoDir,
		worktreePath: tmpDir,
	}, nil
}

// Path returns the filesystem path to the worktree.
func (w *Worktree) Path() string {
	return w.worktreePath
}

// Remove cleans up the worktree and temporary directory.
func (w *Worktree) Remove() error {
	if w == nil || w.worktreePath == "" {
		return nil
	}

	// Remove the git worktree reference.
	cmd := exec.Command("git", "worktree", "remove", "--force", w.worktreePath)
	cmd.Dir = w.repoDir
	_ = cmd.Run() // best-effort — if it fails, manual cleanup needed

	// Also remove the temp directory if it still exists.
	return os.RemoveAll(w.worktreePath)
}
