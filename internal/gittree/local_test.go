package gittree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initTestRepo creates a temporary git repo with some files for testing.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Init git repo.
	run(t, dir, "git", "init", "--initial-branch=main")
	run(t, dir, "git", "config", "user.email", "test@test.com")
	run(t, dir, "git", "config", "user.name", "Test")

	// Create files.
	must(t, os.MkdirAll(filepath.Join(dir, "src"), 0o755))
	must(t, os.MkdirAll(filepath.Join(dir, "docs"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "Makefile"), []byte("all: build"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "src", "handler.go"), []byte("package main"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "docs", "guide.md"), []byte("# guide"), 0o644))

	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-m", "initial commit")

	return dir
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestLocalAdapter_Available(t *testing.T) {
	dir := initTestRepo(t)
	a := NewLocalAdapter()

	if !a.Available(dir) {
		t.Error("Available() should return true for a git repo")
	}
	if a.Available(t.TempDir()) {
		t.Error("Available() should return false for a non-repo dir")
	}
}

func TestLocalAdapter_ListFiles_Root(t *testing.T) {
	dir := initTestRepo(t)
	a := NewLocalAdapter()

	entries, err := a.ListFiles(dir, "", "")
	if err != nil {
		t.Fatalf("ListFiles() error: %v", err)
	}

	// Should have: Makefile, README.md (files) + docs, src (dirs)
	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Path] = true
		if e.Path == "src" && !e.IsDir {
			t.Error("src should be a directory")
		}
		if e.Path == "docs" && !e.IsDir {
			t.Error("docs should be a directory")
		}
		if e.Path == "README.md" && e.IsDir {
			t.Error("README.md should not be a directory")
		}
	}

	for _, want := range []string{"Makefile", "README.md", "src", "docs"} {
		if !names[want] {
			t.Errorf("missing %q in root listing, got: %v", want, names)
		}
	}
}

func TestLocalAdapter_ListFiles_Subdir(t *testing.T) {
	dir := initTestRepo(t)
	a := NewLocalAdapter()

	entries, err := a.ListFiles(dir, "", "src")
	if err != nil {
		t.Fatalf("ListFiles(src) error: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 files in src/, got %d", len(entries))
	}

	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Path] = true
		if e.IsDir {
			t.Errorf("unexpected dir in src/: %s", e.Path)
		}
	}
	if !names["src/main.go"] {
		t.Error("missing src/main.go")
	}
	if !names["src/handler.go"] {
		t.Error("missing src/handler.go")
	}
}

func TestLocalAdapter_LastCommitForFiles(t *testing.T) {
	dir := initTestRepo(t)
	a := NewLocalAdapter()

	commits, err := a.LastCommitForFiles(dir, "", []string{"README.md", "src/main.go"})
	if err != nil {
		t.Fatalf("LastCommitForFiles() error: %v", err)
	}

	if len(commits) != 2 {
		t.Fatalf("expected 2 commits, got %d", len(commits))
	}

	for _, fc := range commits {
		if fc.SHA == "" {
			t.Errorf("empty SHA for %s", fc.Path)
		}
		if fc.Author != "Test" {
			t.Errorf("Author = %q, want Test", fc.Author)
		}
		if fc.Message != "initial commit" {
			t.Errorf("Message = %q, want 'initial commit'", fc.Message)
		}
	}
}

func TestLocalAdapter_LastCommitForFiles_MultipleCommits(t *testing.T) {
	dir := initTestRepo(t)

	// Make a second commit modifying only README.md.
	must(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# updated"), 0o644))
	run(t, dir, "git", "add", "README.md")
	run(t, dir, "git", "commit", "-m", "update readme")

	a := NewLocalAdapter()
	commits, err := a.LastCommitForFiles(dir, "", []string{"README.md", "src/main.go"})
	if err != nil {
		t.Fatalf("LastCommitForFiles() error: %v", err)
	}

	commitMap := make(map[string]FileCommit)
	for _, fc := range commits {
		commitMap[fc.Path] = fc
	}

	if commitMap["README.md"].Message != "update readme" {
		t.Errorf("README.md should have 'update readme', got %q", commitMap["README.md"].Message)
	}
	if commitMap["src/main.go"].Message != "initial commit" {
		t.Errorf("src/main.go should have 'initial commit', got %q", commitMap["src/main.go"].Message)
	}
}
