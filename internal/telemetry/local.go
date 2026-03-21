package telemetry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const telemetryFileName = "telemetry.jsonl"

// LocalCollector implements Collector by appending events as JSON lines
// to a local file (default: ~/.aisync/telemetry.jsonl).
//
// Each call to Track writes immediately — no buffering. This keeps the
// implementation simple and crash-safe. Flush is a no-op.
type LocalCollector struct {
	mu   sync.Mutex
	path string
}

// NewLocalCollector creates a LocalCollector that writes to
// <configDir>/telemetry.jsonl. The config directory is created if it
// does not exist.
func NewLocalCollector(configDir string) *LocalCollector {
	return &LocalCollector{
		path: filepath.Join(configDir, telemetryFileName),
	}
}

// Track appends a JSON-encoded event as a single line to the JSONL file.
// Errors during write are silently ignored — telemetry must never break
// the main application flow.
func (c *LocalCollector) Track(event Event) {
	data, err := json.Marshal(event)
	if err != nil {
		return // silently ignore marshal errors
	}
	data = append(data, '\n')

	c.mu.Lock()
	defer c.mu.Unlock()

	// Ensure directory exists.
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}

	f, err := os.OpenFile(c.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	_, _ = f.Write(data)
}

// Flush is a no-op — writes are immediate.
func (c *LocalCollector) Flush() error { return nil }

// Enabled always returns true for a local collector.
func (c *LocalCollector) Enabled() bool { return true }

// Path returns the file path used for telemetry storage.
// Exported for testing purposes only.
func (c *LocalCollector) Path() string { return c.path }

// ReadEvents reads all events from the JSONL file.
// This is intended for testing and diagnostic use only.
func ReadEvents(path string) ([]Event, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading telemetry file: %w", err)
	}

	var events []Event
	// Split by newlines and parse each JSON line.
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("parsing telemetry event: %w", err)
		}
		events = append(events, e)
	}

	return events, nil
}

// splitLines splits raw bytes by newline, returning non-empty slices.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				lines = append(lines, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
