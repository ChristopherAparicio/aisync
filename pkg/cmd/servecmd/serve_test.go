package servecmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

func testFactory() *cmdutil.Factory {
	return &cmdutil.Factory{IOStreams: iostreams.Test()}
}

func TestNewCmdServe_use(t *testing.T) {
	cmd := NewCmdServe(testFactory())
	if cmd.Use != "serve" {
		t.Fatalf("expected Use %q, got %q", "serve", cmd.Use)
	}
}

func TestNewCmdServe_flags(t *testing.T) {
	cmd := NewCmdServe(testFactory())

	addr := cmd.Flags().Lookup("addr")
	if addr == nil {
		t.Fatal("expected --addr flag to exist")
	}
	if addr.DefValue != defaultAddr {
		t.Fatalf("expected --addr default %q, got %q", defaultAddr, addr.DefValue)
	}

	webOnly := cmd.Flags().Lookup("web-only")
	if webOnly == nil {
		t.Fatal("expected --web-only flag to exist")
	}
	if webOnly.DefValue != "false" {
		t.Fatalf("expected --web-only default %q, got %q", "false", webOnly.DefValue)
	}

	daemon := cmd.Flags().Lookup("daemon")
	if daemon == nil {
		t.Fatal("expected --daemon flag to exist")
	}
	if daemon.DefValue != "false" {
		t.Fatalf("expected --daemon default %q, got %q", "false", daemon.DefValue)
	}

	stop := cmd.Flags().Lookup("stop")
	if stop == nil {
		t.Fatal("expected --stop flag to exist")
	}
	if stop.DefValue != "false" {
		t.Fatalf("expected --stop default %q, got %q", "false", stop.DefValue)
	}
}

// ── PID file helper tests ──

func TestWritePIDFile_and_ReadPIDFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.pid")

	if err := writePIDFileAt(path, 12345); err != nil {
		t.Fatalf("writePIDFileAt: %v", err)
	}

	pid, err := readPIDFileAt(path)
	if err != nil {
		t.Fatalf("readPIDFileAt: %v", err)
	}
	if pid != 12345 {
		t.Fatalf("expected PID 12345, got %d", pid)
	}
}

func TestWritePIDFile_overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.pid")

	if err := writePIDFileAt(path, 111); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writePIDFileAt(path, 222); err != nil {
		t.Fatalf("second write: %v", err)
	}

	pid, err := readPIDFileAt(path)
	if err != nil {
		t.Fatalf("readPIDFileAt: %v", err)
	}
	if pid != 222 {
		t.Fatalf("expected PID 222 after overwrite, got %d", pid)
	}
}

func TestReadPIDFile_nonexistent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.pid")

	_, err := readPIDFileAt(path)
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestReadPIDFile_malformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.pid")

	if err := os.WriteFile(path, []byte("not-a-number\n"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	_, err := readPIDFileAt(path)
	if err == nil {
		t.Fatal("expected error for malformed PID, got nil")
	}
}

func TestReadPIDFile_zeroPID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zero.pid")

	if err := os.WriteFile(path, []byte("0\n"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	_, err := readPIDFileAt(path)
	if err == nil {
		t.Fatal("expected error for zero PID, got nil")
	}
}

func TestReadPIDFile_negativePID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "negative.pid")

	if err := os.WriteFile(path, []byte("-1\n"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	_, err := readPIDFileAt(path)
	if err == nil {
		t.Fatal("expected error for negative PID, got nil")
	}
}

func TestRemovePIDFile_existing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.pid")

	if err := writePIDFileAt(path, 99999); err != nil {
		t.Fatalf("writePIDFileAt: %v", err)
	}

	if err := removePIDFileAt(path); err != nil {
		t.Fatalf("removePIDFileAt: %v", err)
	}

	// Verify file was actually removed.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("expected PID file to be removed")
	}
}

func TestRemovePIDFile_nonexistent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.pid")

	// Should not error when file doesn't exist.
	if err := removePIDFileAt(path); err != nil {
		t.Fatalf("removePIDFileAt on nonexistent file: %v", err)
	}
}

func TestWritePIDFile_fileContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.pid")

	if err := writePIDFileAt(path, 42); err != nil {
		t.Fatalf("writePIDFileAt: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	expected := "42\n"
	if string(data) != expected {
		t.Fatalf("expected file content %q, got %q", expected, string(data))
	}
}

func TestReadPIDFile_whitespace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ws.pid")

	// PID with extra whitespace/newlines should still parse.
	if err := os.WriteFile(path, []byte("  54321  \n"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	pid, err := readPIDFileAt(path)
	if err != nil {
		t.Fatalf("readPIDFileAt: %v", err)
	}
	if pid != 54321 {
		t.Fatalf("expected PID 54321, got %d", pid)
	}
}

func TestWritePIDFile_largePID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.pid")

	largePID := 4194304 // common max PID on Linux
	if err := writePIDFileAt(path, largePID); err != nil {
		t.Fatalf("writePIDFileAt: %v", err)
	}

	pid, err := readPIDFileAt(path)
	if err != nil {
		t.Fatalf("readPIDFileAt: %v", err)
	}
	if pid != largePID {
		t.Fatalf("expected PID %d, got %d", largePID, pid)
	}
}

func TestRemovePIDFile_roundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "roundtrip.pid")

	// Write → Read → Remove → Read should fail.
	if err := writePIDFileAt(path, 777); err != nil {
		t.Fatalf("write: %v", err)
	}

	pid, err := readPIDFileAt(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if pid != 777 {
		t.Fatalf("expected 777, got %d", pid)
	}

	if err := removePIDFileAt(path); err != nil {
		t.Fatalf("remove: %v", err)
	}

	_, err = readPIDFileAt(path)
	if err == nil {
		t.Fatal("expected error reading removed PID file")
	}
}
