package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCurrentBranch(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	branch, err := client.CurrentBranch()
	if err != nil {
		t.Fatalf("CurrentBranch() error: %v", err)
	}
	// Default branch varies (main/master), just check non-empty
	if branch == "" {
		t.Error("CurrentBranch() returned empty string")
	}
}

func TestTopLevel(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	topLevel, err := client.TopLevel()
	if err != nil {
		t.Fatalf("TopLevel() error: %v", err)
	}
	if topLevel == "" {
		t.Error("TopLevel() returned empty string")
	}
}

func TestIsRepo_true(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	if !client.IsRepo() {
		t.Error("IsRepo() = false, want true")
	}
}

func TestIsRepo_false(t *testing.T) {
	dir := t.TempDir()
	client := NewClient(dir)

	if client.IsRepo() {
		t.Error("IsRepo() = true, want false")
	}
}

func TestHooksPath(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	hooksPath, err := client.HooksPath()
	if err != nil {
		t.Fatalf("HooksPath() error: %v", err)
	}
	if hooksPath == "" {
		t.Error("HooksPath() returned empty string")
	}
}

func TestHookExists_notInstalled(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	if client.HookExists("pre-commit") {
		t.Error("HookExists(pre-commit) = true, want false")
	}
}

func TestHookExists_installed(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	// Install a fake hook with "aisync" in it
	hooksPath, err := client.HooksPath()
	if err != nil {
		t.Fatalf("HooksPath() error: %v", err)
	}
	if mkErr := os.MkdirAll(hooksPath, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll() error: %v", mkErr)
	}
	hookFile := filepath.Join(hooksPath, "pre-commit")
	if writeErr := os.WriteFile(hookFile, []byte("#!/bin/sh\n# aisync capture --auto\n"), 0o755); writeErr != nil {
		t.Fatalf("WriteFile() error: %v", writeErr)
	}

	if !client.HookExists("pre-commit") {
		t.Error("HookExists(pre-commit) = false, want true")
	}
}

func TestHeadCommitSHA(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	sha, err := client.HeadCommitSHA()
	if err != nil {
		t.Fatalf("HeadCommitSHA() error: %v", err)
	}
	if sha == "" {
		t.Error("HeadCommitSHA() returned empty string")
	}
	if len(sha) != 40 {
		t.Errorf("HeadCommitSHA() returned %q, want 40-char SHA", sha)
	}
}

func TestAddNote_and_GetNote(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	sha, err := client.HeadCommitSHA()
	if err != nil {
		t.Fatal(err)
	}

	noteContent := `{"session_id":"abc123","provider":"claude-code"}`
	addErr := client.AddNote(sha, noteContent)
	if addErr != nil {
		t.Fatalf("AddNote() error: %v", addErr)
	}

	got, getErr := client.GetNote(sha)
	if getErr != nil {
		t.Fatalf("GetNote() error: %v", getErr)
	}
	if got != noteContent {
		t.Errorf("GetNote() = %q, want %q", got, noteContent)
	}
}

func TestGetNote_noNoteExists(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	sha, err := client.HeadCommitSHA()
	if err != nil {
		t.Fatal(err)
	}

	got, err := client.GetNote(sha)
	if err != nil {
		t.Fatalf("GetNote() error: %v", err)
	}
	if got != "" {
		t.Errorf("GetNote() = %q, want empty string", got)
	}
}

// ── ParseSessionTrailer ──

func TestParseSessionTrailer(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    string
	}{
		{
			name:    "with trailer",
			message: "Add feature X\n\nSigned-off-by: Alice\nAI-Session: abc-123-def\n",
			want:    "abc-123-def",
		},
		{
			name:    "without trailer",
			message: "Regular commit message\n\nSigned-off-by: Alice\n",
			want:    "",
		},
		{
			name:    "empty message",
			message: "",
			want:    "",
		},
		{
			name:    "trailer with whitespace",
			message: "msg\n\nAI-Session:   spaces-around   \n",
			want:    "spaces-around",
		},
		{
			name:    "trailer at first line",
			message: "AI-Session: inline-id",
			want:    "inline-id",
		},
		{
			name:    "multiple trailers picks first",
			message: "msg\n\nAI-Session: first\nAI-Session: second\n",
			want:    "first",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseSessionTrailer(tt.message)
			if got != tt.want {
				t.Errorf("ParseSessionTrailer() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCommitMessage_and_IsValidCommit(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	sha, err := client.HeadCommitSHA()
	if err != nil {
		t.Fatal(err)
	}

	// IsValidCommit should return true for HEAD
	if !client.IsValidCommit(sha) {
		t.Errorf("IsValidCommit(%q) = false, want true", sha)
	}

	// IsValidCommit should return false for garbage
	if client.IsValidCommit("not-a-commit") {
		t.Error("IsValidCommit(not-a-commit) = true, want false")
	}

	// CommitMessage should return the commit message
	msg, err := client.CommitMessage(sha)
	if err != nil {
		t.Fatalf("CommitMessage() error: %v", err)
	}
	if msg == "" {
		t.Error("CommitMessage() returned empty string")
	}
}

// ── Checkout ──

func TestCheckout_success(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	// Create a file so we can test checkout restoring it
	filePath := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(filePath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "hello.txt")
	runGit(t, dir, "commit", "-m", "add hello")

	// Modify the file in the working tree (unstaged)
	if err := os.WriteFile(filePath, []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Checkout should restore the file
	if err := client.Checkout("hello.txt"); err != nil {
		t.Fatalf("Checkout() error: %v", err)
	}

	got, _ := os.ReadFile(filePath)
	if string(got) != "original" {
		t.Errorf("after Checkout, file content = %q, want %q", got, "original")
	}
}

func TestCheckout_error(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	// Checkout a non-existent file should error
	err := client.Checkout("nonexistent-file-that-does-not-exist.txt")
	if err == nil {
		t.Fatal("expected error from Checkout on nonexistent file")
	}
}

// ── UserName / UserEmail ──

func TestUserName(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	name := client.UserName()
	if name != "Test" {
		t.Errorf("UserName() = %q, want %q", name, "Test")
	}
}

func TestUserName_notConfigured(t *testing.T) {
	dir := t.TempDir()
	// Init repo without setting user.name
	runGit(t, dir, "init")

	client := NewClient(dir)
	name := client.UserName()
	// Should return empty string without error (may return global config value)
	_ = name // no panic is the assertion
}

func TestUserEmail(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	email := client.UserEmail()
	if email != "test@test.com" {
		t.Errorf("UserEmail() = %q, want %q", email, "test@test.com")
	}
}

func TestUserEmail_notConfigured(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")

	client := NewClient(dir)
	email := client.UserEmail()
	_ = email
}

// ── HasRemote / RemoteURL ──

func TestHasRemote_false(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	if client.HasRemote("origin") {
		t.Error("HasRemote(origin) = true, want false (no remote configured)")
	}
}

func TestHasRemote_true(t *testing.T) {
	dir := initTestRepo(t)
	// Add a fake remote
	runGit(t, dir, "remote", "add", "origin", "https://example.com/repo.git")
	client := NewClient(dir)

	if !client.HasRemote("origin") {
		t.Error("HasRemote(origin) = false, want true")
	}
}

func TestRemoteURL_noRemote(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	url := client.RemoteURL("origin")
	if url != "" {
		t.Errorf("RemoteURL(origin) = %q, want empty string", url)
	}
}

func TestRemoteURL_withRemote(t *testing.T) {
	dir := initTestRepo(t)
	runGit(t, dir, "remote", "add", "origin", "https://example.com/repo.git")
	client := NewClient(dir)

	url := client.RemoteURL("origin")
	if url != "https://example.com/repo.git" {
		t.Errorf("RemoteURL(origin) = %q, want %q", url, "https://example.com/repo.git")
	}
}

// ── SyncBranch operations ──

func TestSyncBranchExists_false(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	if client.SyncBranchExists() {
		t.Error("SyncBranchExists() = true, want false")
	}
}

func TestInitSyncBranch_and_SyncBranchExists(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	if err := client.InitSyncBranch(); err != nil {
		t.Fatalf("InitSyncBranch() error: %v", err)
	}

	if !client.SyncBranchExists() {
		t.Error("SyncBranchExists() = false after InitSyncBranch()")
	}
}

func TestListSyncFiles_empty(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	// Without sync branch, should return nil
	files, err := client.ListSyncFiles()
	if err != nil {
		t.Fatalf("ListSyncFiles() error: %v", err)
	}
	if files != nil {
		t.Errorf("ListSyncFiles() = %v, want nil", files)
	}
}

func TestWriteSyncFiles_and_ReadSyncFile(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	if err := client.InitSyncBranch(); err != nil {
		t.Fatalf("InitSyncBranch() error: %v", err)
	}

	// Write files
	files := map[string][]byte{
		"sessions/abc.json": []byte(`{"id":"abc","title":"test"}`),
		"sessions/def.json": []byte(`{"id":"def","title":"other"}`),
	}
	if err := client.WriteSyncFiles(files, "add test sessions"); err != nil {
		t.Fatalf("WriteSyncFiles() error: %v", err)
	}

	// Read back
	data, err := client.ReadSyncFile("sessions/abc.json")
	if err != nil {
		t.Fatalf("ReadSyncFile() error: %v", err)
	}
	if string(data) != `{"id":"abc","title":"test"}` {
		t.Errorf("ReadSyncFile() = %q, want %q", data, `{"id":"abc","title":"test"}`)
	}

	// Read non-existent file
	data, err = client.ReadSyncFile("nonexistent.json")
	if err != nil {
		t.Fatalf("ReadSyncFile(nonexistent) error: %v", err)
	}
	if data != nil {
		t.Errorf("ReadSyncFile(nonexistent) = %v, want nil", data)
	}
}

func TestWriteSyncFiles_emptyMap(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	// Empty map should be a no-op
	err := client.WriteSyncFiles(map[string][]byte{}, "nothing")
	if err != nil {
		t.Fatalf("WriteSyncFiles(empty) error: %v", err)
	}
}

func TestListSyncFiles_withFiles(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	if err := client.InitSyncBranch(); err != nil {
		t.Fatal(err)
	}

	files := map[string][]byte{
		"a.json": []byte("a"),
		"b.json": []byte("b"),
	}
	if err := client.WriteSyncFiles(files, "add files"); err != nil {
		t.Fatal(err)
	}

	list, err := client.ListSyncFiles()
	if err != nil {
		t.Fatalf("ListSyncFiles() error: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("ListSyncFiles() returned %d files, want 2", len(list))
	}
}

// ── Push/Pull SyncBranch ──

func TestPushSyncBranch_noRemote(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	err := client.PushSyncBranch("")
	if err == nil {
		t.Fatal("expected error pushing without remote")
	}
}

func TestPullSyncBranch_noRemote(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	err := client.PullSyncBranch("")
	if err == nil {
		t.Fatal("expected error pulling without remote")
	}
}

func TestPushPullSyncBranch_withLocalRemote(t *testing.T) {
	// Create a bare "remote" repo
	remoteDir := t.TempDir()
	runGit(t, remoteDir, "init", "--bare")

	// Create a local repo with origin pointing to the bare remote
	dir := initTestRepo(t)
	runGit(t, dir, "remote", "add", "origin", remoteDir)

	client := NewClient(dir)

	// Init sync branch and write something
	if err := client.InitSyncBranch(); err != nil {
		t.Fatal(err)
	}
	if err := client.WriteSyncFiles(map[string][]byte{"test.json": []byte("hello")}, "test"); err != nil {
		t.Fatal(err)
	}

	// Push should succeed
	if err := client.PushSyncBranch("origin"); err != nil {
		t.Fatalf("PushSyncBranch() error: %v", err)
	}

	// Create another local clone from the bare repo to test pull
	dir2 := t.TempDir()
	cmd := exec.Command("git", "clone", remoteDir, dir2)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone failed: %v\n%s", err, out)
	}

	client2 := NewClient(dir2)
	if err := client2.PullSyncBranch("origin"); err != nil {
		t.Fatalf("PullSyncBranch() error: %v", err)
	}

	// Verify the data is there
	data, err := client2.ReadSyncFile("test.json")
	if err != nil {
		t.Fatalf("ReadSyncFile() error: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("ReadSyncFile() = %q, want %q", data, "hello")
	}
}

// ── HooksPath with custom core.hooksPath ──

func TestHooksPath_customAbsolute(t *testing.T) {
	dir := initTestRepo(t)
	customPath := filepath.Join(dir, "custom-hooks")
	runGit(t, dir, "config", "core.hooksPath", customPath)

	client := NewClient(dir)
	got, err := client.HooksPath()
	if err != nil {
		t.Fatalf("HooksPath() error: %v", err)
	}
	if got != customPath {
		t.Errorf("HooksPath() = %q, want %q", got, customPath)
	}
}

func TestHooksPath_customRelative(t *testing.T) {
	dir := initTestRepo(t)
	runGit(t, dir, "config", "core.hooksPath", "my-hooks")

	client := NewClient(dir)
	got, err := client.HooksPath()
	if err != nil {
		t.Fatalf("HooksPath() error: %v", err)
	}
	expected := filepath.Join(dir, "my-hooks")
	if got != expected {
		t.Errorf("HooksPath() = %q, want %q", got, expected)
	}
}

// ── Error paths (non-repo directory) ──

func TestCurrentBranch_notARepo(t *testing.T) {
	dir := t.TempDir()
	client := NewClient(dir)

	_, err := client.CurrentBranch()
	if err == nil {
		t.Fatal("expected error from CurrentBranch on non-repo")
	}
}

func TestTopLevel_notARepo(t *testing.T) {
	dir := t.TempDir()
	client := NewClient(dir)

	_, err := client.TopLevel()
	if err == nil {
		t.Fatal("expected error from TopLevel on non-repo")
	}
}

func TestHeadCommitSHA_notARepo(t *testing.T) {
	dir := t.TempDir()
	client := NewClient(dir)

	_, err := client.HeadCommitSHA()
	if err == nil {
		t.Fatal("expected error from HeadCommitSHA on non-repo")
	}
}

func TestCommitMessage_notARepo(t *testing.T) {
	dir := t.TempDir()
	client := NewClient(dir)

	_, err := client.CommitMessage("HEAD")
	if err == nil {
		t.Fatal("expected error from CommitMessage on non-repo")
	}
}

func TestAddNote_notARepo(t *testing.T) {
	dir := t.TempDir()
	client := NewClient(dir)

	err := client.AddNote("abc123", "content")
	if err == nil {
		t.Fatal("expected error from AddNote on non-repo")
	}
}

func TestHooksPath_notARepo(t *testing.T) {
	dir := t.TempDir()
	client := NewClient(dir)

	_, err := client.HooksPath()
	if err == nil {
		t.Fatal("expected error from HooksPath on non-repo")
	}
}

func TestHookExists_notARepo(t *testing.T) {
	dir := t.TempDir()
	client := NewClient(dir)

	// Should return false without panic
	if client.HookExists("pre-commit") {
		t.Error("HookExists on non-repo should return false")
	}
}

// ── SwitchBranch ──

func TestSwitchBranch_success(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	// Create a branch to switch to.
	runGit(t, dir, "branch", "feature/test")

	if err := client.SwitchBranch("feature/test"); err != nil {
		t.Fatalf("SwitchBranch() error: %v", err)
	}

	branch, err := client.CurrentBranch()
	if err != nil {
		t.Fatalf("CurrentBranch() error: %v", err)
	}
	if branch != "feature/test" {
		t.Errorf("CurrentBranch() = %q, want %q", branch, "feature/test")
	}
}

func TestSwitchBranch_nonexistent(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	err := client.SwitchBranch("nonexistent-branch")
	if err == nil {
		t.Fatal("expected error when switching to nonexistent branch")
	}
}

// ── WorktreeAdd / WorktreeRemove ──

func TestWorktreeAdd_and_Remove(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	sha, err := client.HeadCommitSHA()
	if err != nil {
		t.Fatalf("HeadCommitSHA() error: %v", err)
	}

	wtPath := filepath.Join(dir, ".worktrees", "test-wt")

	// Add worktree at HEAD commit.
	if err := client.WorktreeAdd(wtPath, sha); err != nil {
		t.Fatalf("WorktreeAdd() error: %v", err)
	}

	// Verify the worktree directory was created.
	if _, statErr := os.Stat(wtPath); os.IsNotExist(statErr) {
		t.Fatal("worktree directory does not exist after WorktreeAdd")
	}

	// Verify it's a git working tree (has a .git file, not directory).
	gitFile := filepath.Join(wtPath, ".git")
	info, statErr := os.Stat(gitFile)
	if statErr != nil {
		t.Fatalf("worktree .git file stat error: %v", statErr)
	}
	if info.IsDir() {
		t.Error("worktree .git should be a file, not a directory")
	}

	// Remove the worktree.
	if err := client.WorktreeRemove(wtPath); err != nil {
		t.Fatalf("WorktreeRemove() error: %v", err)
	}

	// Verify the directory is removed.
	if _, statErr := os.Stat(wtPath); !os.IsNotExist(statErr) {
		t.Error("worktree directory still exists after WorktreeRemove")
	}
}

func TestWorktreeAdd_defaultRef(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	wtPath := filepath.Join(dir, ".worktrees", "head-wt")

	// Empty ref should default to HEAD.
	if err := client.WorktreeAdd(wtPath, ""); err != nil {
		t.Fatalf("WorktreeAdd(ref='') error: %v", err)
	}

	if _, statErr := os.Stat(wtPath); os.IsNotExist(statErr) {
		t.Fatal("worktree directory does not exist after WorktreeAdd with empty ref")
	}

	// Cleanup.
	_ = client.WorktreeRemove(wtPath)
}

func TestWorktreeAdd_invalidRef(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	wtPath := filepath.Join(dir, ".worktrees", "bad-ref-wt")

	err := client.WorktreeAdd(wtPath, "invalid-ref-that-does-not-exist")
	if err == nil {
		t.Fatal("expected error when adding worktree with invalid ref")
	}
}

func TestWorktreeRemove_nonexistent(t *testing.T) {
	dir := initTestRepo(t)
	client := NewClient(dir)

	err := client.WorktreeRemove(filepath.Join(dir, "nonexistent-worktree"))
	if err == nil {
		t.Fatal("expected error when removing nonexistent worktree")
	}
}

// ── helper ──

// runGit is a test helper that runs a git command in a directory.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

// initTestRepo creates a temporary git repository for testing.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	commands := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "commit", "--allow-empty", "-m", "initial"},
	}

	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}

	return dir
}
