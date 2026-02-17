package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstall(t *testing.T) {
	t.Run("installs all hooks into empty directory", func(t *testing.T) {
		dir := t.TempDir()
		mgr := NewManager(dir)

		if err := mgr.Install(); err != nil {
			t.Fatalf("Install() error = %v", err)
		}

		for _, name := range hookNames {
			hookPath := filepath.Join(dir, name)
			data, err := os.ReadFile(hookPath)
			if err != nil {
				t.Errorf("hook %s not created: %v", name, err)
				continue
			}

			content := string(data)
			if !strings.Contains(content, startMarker) {
				t.Errorf("hook %s missing start marker", name)
			}
			if !strings.Contains(content, endMarker) {
				t.Errorf("hook %s missing end marker", name)
			}
			if !strings.HasPrefix(content, "#!/usr/bin/env bash") {
				t.Errorf("hook %s missing shebang", name)
			}

			// Verify executable permissions
			info, _ := os.Stat(hookPath)
			if info.Mode()&0o111 == 0 {
				t.Errorf("hook %s is not executable", name)
			}
		}
	})

	t.Run("chains with existing hook", func(t *testing.T) {
		dir := t.TempDir()
		existingContent := "#!/usr/bin/env bash\necho 'existing pre-commit hook'\n"
		hookPath := filepath.Join(dir, "pre-commit")
		if err := os.WriteFile(hookPath, []byte(existingContent), 0o755); err != nil {
			t.Fatal(err)
		}

		mgr := NewManager(dir)
		if err := mgr.Install(); err != nil {
			t.Fatalf("Install() error = %v", err)
		}

		data, err := os.ReadFile(hookPath)
		if err != nil {
			t.Fatal(err)
		}
		content := string(data)

		// Should preserve existing content
		if !strings.Contains(content, "existing pre-commit hook") {
			t.Error("existing hook content was lost")
		}

		// Should contain aisync section
		if !strings.Contains(content, startMarker) {
			t.Error("aisync section not added")
		}
	})

	t.Run("replaces existing aisync section on reinstall", func(t *testing.T) {
		dir := t.TempDir()
		mgr := NewManager(dir)

		// First install
		if err := mgr.Install(); err != nil {
			t.Fatal(err)
		}

		hookPath := filepath.Join(dir, "pre-commit")
		dataBefore, _ := os.ReadFile(hookPath)

		// Second install (should replace, not duplicate)
		if err := mgr.Install(); err != nil {
			t.Fatal(err)
		}

		dataAfter, _ := os.ReadFile(hookPath)

		// Count markers — should appear exactly once
		beforeCount := strings.Count(string(dataBefore), startMarker)
		afterCount := strings.Count(string(dataAfter), startMarker)

		if beforeCount != 1 {
			t.Errorf("before: expected 1 start marker, got %d", beforeCount)
		}
		if afterCount != 1 {
			t.Errorf("after: expected 1 start marker, got %d", afterCount)
		}
	})
}

func TestUninstall(t *testing.T) {
	t.Run("removes aisync section and file if only aisync content", func(t *testing.T) {
		dir := t.TempDir()
		mgr := NewManager(dir)

		if err := mgr.Install(); err != nil {
			t.Fatal(err)
		}

		if err := mgr.Uninstall(); err != nil {
			t.Fatalf("Uninstall() error = %v", err)
		}

		for _, name := range hookNames {
			hookPath := filepath.Join(dir, name)
			if _, err := os.Stat(hookPath); !os.IsNotExist(err) {
				t.Errorf("hook %s still exists after uninstall (was aisync-only)", name)
			}
		}
	})

	t.Run("preserves non-aisync content in hook", func(t *testing.T) {
		dir := t.TempDir()
		existingContent := "#!/usr/bin/env bash\necho 'my custom hook'\n"
		hookPath := filepath.Join(dir, "pre-commit")
		if err := os.WriteFile(hookPath, []byte(existingContent), 0o755); err != nil {
			t.Fatal(err)
		}

		mgr := NewManager(dir)
		if err := mgr.Install(); err != nil {
			t.Fatal(err)
		}
		if err := mgr.Uninstall(); err != nil {
			t.Fatal(err)
		}

		data, err := os.ReadFile(hookPath)
		if err != nil {
			t.Fatalf("hook file removed despite having non-aisync content: %v", err)
		}
		content := string(data)

		if strings.Contains(content, startMarker) {
			t.Error("aisync section still present")
		}
		if !strings.Contains(content, "my custom hook") {
			t.Error("custom hook content was removed")
		}
	})

	t.Run("no-op if hooks not installed", func(t *testing.T) {
		dir := t.TempDir()
		mgr := NewManager(dir)

		if err := mgr.Uninstall(); err != nil {
			t.Fatalf("Uninstall() on empty dir should not error: %v", err)
		}
	})
}

func TestStatuses(t *testing.T) {
	t.Run("returns installed status", func(t *testing.T) {
		dir := t.TempDir()
		mgr := NewManager(dir)

		// Before install
		for _, s := range mgr.Statuses() {
			if s.Installed {
				t.Errorf("hook %s should not be installed before Install()", s.Name)
			}
		}

		if err := mgr.Install(); err != nil {
			t.Fatal(err)
		}

		// After install
		for _, s := range mgr.Statuses() {
			if !s.Installed {
				t.Errorf("hook %s should be installed after Install()", s.Name)
			}
		}
	})
}

func TestIsInstalled(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	if mgr.IsInstalled("pre-commit") {
		t.Error("pre-commit should not be installed initially")
	}

	if err := mgr.Install(); err != nil {
		t.Fatal(err)
	}

	if !mgr.IsInstalled("pre-commit") {
		t.Error("pre-commit should be installed after Install()")
	}
}

func TestHookNames(t *testing.T) {
	names := HookNames()
	if len(names) != 3 {
		t.Fatalf("expected 3 hook names, got %d", len(names))
	}

	expected := map[string]bool{
		"pre-commit":    true,
		"commit-msg":    true,
		"post-checkout": true,
	}

	for _, name := range names {
		if !expected[name] {
			t.Errorf("unexpected hook name: %s", name)
		}
	}
}

func TestPreCommitHookContent(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	if err := mgr.Install(); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "pre-commit"))
	content := string(data)

	if !strings.Contains(content, "aisync capture --auto") {
		t.Error("pre-commit hook should contain 'aisync capture --auto'")
	}
}

func TestCommitMsgHookContent(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	if err := mgr.Install(); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "commit-msg"))
	content := string(data)

	if !strings.Contains(content, "AI-Session:") {
		t.Error("commit-msg hook should contain 'AI-Session:' trailer logic")
	}
	if !strings.Contains(content, "COMMIT_MSG_FILE") {
		t.Error("commit-msg hook should reference COMMIT_MSG_FILE")
	}
}

func TestPostCheckoutHookContent(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	if err := mgr.Install(); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "post-checkout"))
	content := string(data)

	if !strings.Contains(content, "BRANCH_CHECKOUT") {
		t.Error("post-checkout hook should check BRANCH_CHECKOUT flag")
	}
	if !strings.Contains(content, "aisync restore") {
		t.Error("post-checkout hook should mention 'aisync restore'")
	}
}

func TestReplaceAisyncSection(t *testing.T) {
	original := "#!/bin/bash\nbefore\n# ── aisync:start ──\nold content\n# ── aisync:end ──\nafter\n"
	newSection := "# ── aisync:start ──\nnew content\n# ── aisync:end ──"

	result := replaceAisyncSection(original, newSection)

	if strings.Contains(result, "old content") {
		t.Error("old content should be replaced")
	}
	if !strings.Contains(result, "new content") {
		t.Error("new content should be present")
	}
	if !strings.Contains(result, "before") {
		t.Error("content before aisync section should be preserved")
	}
	if !strings.Contains(result, "after") {
		t.Error("content after aisync section should be preserved")
	}
}

func TestRemoveAisyncSection(t *testing.T) {
	content := "#!/bin/bash\nbefore\n# ── aisync:start ──\naisync stuff\n# ── aisync:end ──\nafter\n"

	result := removeAisyncSection(content)

	if strings.Contains(result, "aisync stuff") {
		t.Error("aisync section should be removed")
	}
	if strings.Contains(result, startMarker) {
		t.Error("start marker should be removed")
	}
	if !strings.Contains(result, "before") {
		t.Error("content before section should be preserved")
	}
	if !strings.Contains(result, "after") {
		t.Error("content after section should be preserved")
	}
}
