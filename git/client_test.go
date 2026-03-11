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
