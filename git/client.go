// Package git provides Git operations for aisync.
// It wraps the git CLI to avoid library dependencies.
package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Client wraps git CLI operations.
type Client struct {
	// repoDir is the root of the git repository.
	repoDir string
}

// NewClient creates a Git client for the given repository directory.
func NewClient(repoDir string) *Client {
	return &Client{repoDir: repoDir}
}

// CurrentBranch returns the name of the current branch.
func (c *Client) CurrentBranch() (string, error) {
	out, err := c.run("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("getting current branch: %w", err)
	}
	return out, nil
}

// TopLevel returns the absolute path to the repository root.
func (c *Client) TopLevel() (string, error) {
	out, err := c.run("rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("getting repo root: %w", err)
	}
	return out, nil
}

// IsRepo returns true if the current directory is inside a git repository.
func (c *Client) IsRepo() bool {
	_, err := c.run("rev-parse", "--git-dir")
	return err == nil
}

// HooksPath returns the path to the git hooks directory.
func (c *Client) HooksPath() (string, error) {
	// First try core.hooksPath config
	out, err := c.run("config", "--get", "core.hooksPath")
	if err == nil && out != "" {
		if filepath.IsAbs(out) {
			return out, nil
		}
		return filepath.Join(c.repoDir, out), nil
	}

	// Default to .git/hooks
	gitDir, err := c.run("rev-parse", "--git-dir")
	if err != nil {
		return "", fmt.Errorf("getting git dir: %w", err)
	}

	hooksDir := filepath.Join(gitDir, "hooks")
	if !filepath.IsAbs(hooksDir) {
		hooksDir = filepath.Join(c.repoDir, hooksDir)
	}

	return hooksDir, nil
}

// HookExists checks if a specific git hook is installed for aisync.
func (c *Client) HookExists(hookName string) bool {
	hooksPath, err := c.HooksPath()
	if err != nil {
		return false
	}

	hookFile := filepath.Join(hooksPath, hookName)
	cmd := exec.Command("grep", "-q", "aisync", hookFile)
	return cmd.Run() == nil
}

// AddNote adds a git note to a commit.
// The notes namespace is "aisync" to avoid conflicts with other tools.
func (c *Client) AddNote(commitSHA, content string) error {
	_, err := c.run("notes", "--ref=aisync", "add", "-f", "-m", content, commitSHA)
	if err != nil {
		return fmt.Errorf("adding git note to %s: %w", commitSHA, err)
	}
	return nil
}

// GetNote retrieves the aisync git note for a commit.
// Returns empty string if no note exists.
func (c *Client) GetNote(commitSHA string) (string, error) {
	out, err := c.run("notes", "--ref=aisync", "show", commitSHA)
	if err != nil {
		// No note found is not an error
		return "", nil
	}
	return out, nil
}

// HeadCommitSHA returns the SHA of the HEAD commit.
func (c *Client) HeadCommitSHA() (string, error) {
	out, err := c.run("rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("getting HEAD commit SHA: %w", err)
	}
	return out, nil
}

// --- Sync branch operations ---
// These methods use git plumbing to read/write files on the aisync/sessions
// branch without touching the working directory.

const syncBranch = "aisync/sessions"

// SyncBranchExists checks if the aisync/sessions branch exists.
func (c *Client) SyncBranchExists() bool {
	_, err := c.run("rev-parse", "--verify", "refs/heads/"+syncBranch)
	return err == nil
}

// InitSyncBranch creates the orphan aisync/sessions branch with an empty initial commit.
func (c *Client) InitSyncBranch() error {
	// Create empty tree
	emptyTree, err := c.run("hash-object", "-t", "tree", "--stdin")
	if err != nil {
		// hash-object with --stdin and tree type needs /dev/null input
		emptyTree, err = c.run("mktree")
		if err != nil {
			return fmt.Errorf("creating empty tree: %w", err)
		}
	}
	if emptyTree == "" {
		// Empty tree has a well-known SHA
		emptyTree = "4b825dc642cb6eb9a060e54bf899d69ef6e79130"
	}

	// Create initial commit on the orphan branch
	commitSHA, err := c.run("commit-tree", "-m", "aisync: initialize session storage", emptyTree)
	if err != nil {
		return fmt.Errorf("creating initial commit: %w", err)
	}

	// Create the branch reference
	_, err = c.run("update-ref", "refs/heads/"+syncBranch, commitSHA)
	if err != nil {
		return fmt.Errorf("creating branch %s: %w", syncBranch, err)
	}

	return nil
}

// ReadSyncFile reads a file from the aisync/sessions branch.
// Returns empty bytes and no error if the file doesn't exist.
func (c *Client) ReadSyncFile(path string) ([]byte, error) {
	out, err := c.runRaw("show", syncBranch+":"+path)
	if err != nil {
		return nil, nil // file not found is not an error
	}
	return out, nil
}

// WriteSyncFiles writes one or more files to the aisync/sessions branch in a single commit.
// files is a map of path -> content.
func (c *Client) WriteSyncFiles(files map[string][]byte, message string) error {
	if len(files) == 0 {
		return nil
	}

	// Get current tree of the sync branch
	currentTree, err := c.run("rev-parse", syncBranch+"^{tree}")
	if err != nil {
		return fmt.Errorf("reading sync branch tree: %w", err)
	}

	parentCommit, err := c.run("rev-parse", syncBranch)
	if err != nil {
		return fmt.Errorf("reading sync branch HEAD: %w", err)
	}

	// Build tree entries: start with existing tree, add/replace files
	// Use read-tree + update-index approach via env var GIT_INDEX_FILE
	indexFile := filepath.Join(c.repoDir, ".git", "aisync-tmp-index")

	// Read existing tree into temp index
	_, err = c.runWithEnv([]string{"GIT_INDEX_FILE=" + indexFile}, "read-tree", currentTree)
	if err != nil {
		return fmt.Errorf("reading tree into index: %w", err)
	}

	// Add each file
	for path, content := range files {
		// Hash the object
		blobSHA, hashErr := c.runWithStdin(content, "hash-object", "-w", "--stdin")
		if hashErr != nil {
			return fmt.Errorf("hashing object for %s: %w", path, hashErr)
		}

		// Update the index entry
		_, updateErr := c.runWithEnv([]string{"GIT_INDEX_FILE=" + indexFile},
			"update-index", "--add", "--cacheinfo", "100644", blobSHA, path)
		if updateErr != nil {
			return fmt.Errorf("updating index for %s: %w", path, updateErr)
		}
	}

	// Write the tree
	newTree, err := c.runWithEnv([]string{"GIT_INDEX_FILE=" + indexFile}, "write-tree")
	if err != nil {
		return fmt.Errorf("writing tree: %w", err)
	}

	// Create commit
	newCommit, err := c.run("commit-tree", "-m", message, "-p", parentCommit, newTree)
	if err != nil {
		return fmt.Errorf("creating commit: %w", err)
	}

	// Update the branch ref
	_, err = c.run("update-ref", "refs/heads/"+syncBranch, newCommit)
	if err != nil {
		return fmt.Errorf("updating ref: %w", err)
	}

	// Clean up temp index
	_ = os.Remove(indexFile)

	return nil
}

// ListSyncFiles lists all files on the aisync/sessions branch.
func (c *Client) ListSyncFiles() ([]string, error) {
	out, err := c.run("ls-tree", "--name-only", "-r", syncBranch)
	if err != nil {
		return nil, nil // branch empty or doesn't exist
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// PushSyncBranch pushes the aisync/sessions branch to the remote.
func (c *Client) PushSyncBranch(remote string) error {
	if remote == "" {
		remote = "origin"
	}
	_, err := c.run("push", remote, syncBranch)
	if err != nil {
		return fmt.Errorf("pushing %s to %s: %w", syncBranch, remote, err)
	}
	return nil
}

// PullSyncBranch pulls the aisync/sessions branch from the remote.
func (c *Client) PullSyncBranch(remote string) error {
	if remote == "" {
		remote = "origin"
	}
	// Fetch the remote branch
	_, err := c.run("fetch", remote, syncBranch+":"+syncBranch)
	if err != nil {
		return fmt.Errorf("fetching %s from %s: %w", syncBranch, remote, err)
	}
	return nil
}

// HasRemote checks if a remote exists.
func (c *Client) HasRemote(name string) bool {
	_, err := c.run("remote", "get-url", name)
	return err == nil
}

// RemoteURL returns the URL of a named remote.
// Returns an empty string if the remote doesn't exist.
func (c *Client) RemoteURL(name string) string {
	url, err := c.run("remote", "get-url", name)
	if err != nil {
		return ""
	}
	return url
}

// runRaw runs a git command and returns raw output bytes (no trimming).
func (c *Client) runRaw(args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = c.repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

// runWithEnv runs a git command with extra environment variables.
func (c *Client) runWithEnv(env []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = c.repoDir
	cmd.Env = append(cmd.Environ(), env...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// runWithStdin runs a git command with data piped to stdin.
func (c *Client) runWithStdin(input []byte, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = c.repoDir
	cmd.Stdin = strings.NewReader(string(input))
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *Client) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = c.repoDir

	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}
