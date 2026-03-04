package scanplugin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	goplugin "github.com/hashicorp/go-plugin"

	pb "github.com/ChristopherAparicio/aisync/internal/secrets/scanplugin/proto"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// TestGRPCPlugin_inProcess uses go-plugin's test mode to validate the gRPC
// plugin architecture works end-to-end without launching a separate process.
func TestGRPCPlugin_inProcess(t *testing.T) {
	// Create a real scanner implementation for testing
	impl := &testScannerImpl{}

	// Use go-plugin's test mode: the plugin runs in-process
	closeCh := make(chan struct{})
	reattachCh := make(chan *goplugin.ReattachConfig, 1)
	serveConfig := &goplugin.ServeConfig{
		HandshakeConfig: HandshakeConfig,
		Plugins: map[string]goplugin.Plugin{
			"scanner": &GRPCPlugin{Impl: impl},
		},
		GRPCServer: goplugin.DefaultGRPCServer,
		Test: &goplugin.ServeTestConfig{
			ReattachConfigCh: reattachCh,
			CloseCh:          closeCh,
		},
	}

	// Start the test server in a goroutine
	go goplugin.Serve(serveConfig)

	// Wait for the reattach config
	reattachCfg := <-reattachCh

	// Create a client that connects to the test server
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig:  HandshakeConfig,
		Plugins:          PluginMap,
		Reattach:         reattachCfg,
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
	})
	defer client.Kill()

	rpcClient, err := client.Client()
	if err != nil {
		t.Fatalf("client.Client() error: %v", err)
	}

	raw, err := rpcClient.Dispense("scanner")
	if err != nil {
		t.Fatalf("Dispense() error: %v", err)
	}

	scanner, ok := raw.(ScannerPlugin)
	if !ok {
		t.Fatalf("dispensed plugin does not implement ScannerPlugin, got %T", raw)
	}

	// --- Test Scan ---
	t.Run("scan detects test secrets", func(t *testing.T) {
		matches, scanErr := scanner.Scan("found TEST_SECRET_ABCDEFGH in code")
		if scanErr != nil {
			t.Fatalf("Scan() error: %v", scanErr)
		}
		if len(matches) != 1 {
			t.Fatalf("expected 1 match, got %d", len(matches))
		}
		if matches[0].Type != "TEST_SECRET" {
			t.Errorf("Type = %q, want TEST_SECRET", matches[0].Type)
		}
		if matches[0].Value != "TEST_SECRET_ABCDEFGH" {
			t.Errorf("Value = %q, want TEST_SECRET_ABCDEFGH", matches[0].Value)
		}
	})

	t.Run("scan returns empty for clean content", func(t *testing.T) {
		matches, scanErr := scanner.Scan("just regular text")
		if scanErr != nil {
			t.Fatalf("Scan() error: %v", scanErr)
		}
		if len(matches) != 0 {
			t.Errorf("expected 0 matches, got %d", len(matches))
		}
	})

	// --- Test Mask ---
	t.Run("mask replaces secrets", func(t *testing.T) {
		masked, maskErr := scanner.Mask("key=TEST_SECRET_ABCDEFGH here")
		if maskErr != nil {
			t.Fatalf("Mask() error: %v", maskErr)
		}
		if strings.Contains(masked, "TEST_SECRET_ABCDEFGH") {
			t.Error("masked content should not contain the secret")
		}
		if !strings.Contains(masked, "***REDACTED:TEST_SECRET***") {
			t.Errorf("masked = %q, want to contain ***REDACTED:TEST_SECRET***", masked)
		}
	})

	t.Run("mask returns unchanged for clean content", func(t *testing.T) {
		input := "just regular text"
		masked, maskErr := scanner.Mask(input)
		if maskErr != nil {
			t.Fatalf("Mask() error: %v", maskErr)
		}
		if masked != input {
			t.Errorf("Mask() = %q, want %q", masked, input)
		}
	})

	t.Run("scan detects multiple secrets", func(t *testing.T) {
		matches, scanErr := scanner.Scan("TEST_SECRET_AAAAAAAA and TEST_SECRET_BBBBBBBB")
		if scanErr != nil {
			t.Fatalf("Scan() error: %v", scanErr)
		}
		if len(matches) != 2 {
			t.Fatalf("expected 2 matches, got %d", len(matches))
		}
	})
}

// TestGRPCAdapter_wrapsPlugin tests the GRPCAdapter wrapping a real gRPC client
// into a SecretScanner using in-process test mode.
func TestGRPCAdapter_wrapsPlugin(t *testing.T) {
	impl := &testScannerImpl{}

	closeCh2 := make(chan struct{})
	reattachCh2 := make(chan *goplugin.ReattachConfig, 1)
	serveConfig := &goplugin.ServeConfig{
		HandshakeConfig: HandshakeConfig,
		Plugins: map[string]goplugin.Plugin{
			"scanner": &GRPCPlugin{Impl: impl},
		},
		GRPCServer: goplugin.DefaultGRPCServer,
		Test: &goplugin.ServeTestConfig{
			ReattachConfigCh: reattachCh2,
			CloseCh:          closeCh2,
		},
	}

	go goplugin.Serve(serveConfig)
	reattachCfg := <-reattachCh2

	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig:  HandshakeConfig,
		Plugins:          PluginMap,
		Reattach:         reattachCfg,
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
	})

	rpcClient, err := client.Client()
	if err != nil {
		t.Fatalf("Client() error: %v", err)
	}

	raw, err := rpcClient.Dispense("scanner")
	if err != nil {
		t.Fatalf("Dispense() error: %v", err)
	}

	scannerPlugin := raw.(ScannerPlugin)

	// Wrap in GRPCAdapter
	adapter := &GRPCAdapter{
		client: client,
		raw:    scannerPlugin,
		mode:   session.SecretModeMask,
	}
	defer adapter.Close()

	// Verify it satisfies SecretScanner
	var _ SecretScanner = adapter

	t.Run("Scan returns session matches", func(t *testing.T) {
		matches := adapter.Scan("TEST_SECRET_ABCDEFGH here")
		if len(matches) != 1 {
			t.Fatalf("expected 1 match, got %d", len(matches))
		}
		if matches[0].Type != "TEST_SECRET" {
			t.Errorf("Type = %q, want TEST_SECRET", matches[0].Type)
		}
	})

	t.Run("Mask returns masked content", func(t *testing.T) {
		masked := adapter.Mask("TEST_SECRET_ABCDEFGH here")
		if !strings.Contains(masked, "***REDACTED:TEST_SECRET***") {
			t.Errorf("Mask() = %q, want redacted", masked)
		}
	})

	t.Run("Mode returns configured mode", func(t *testing.T) {
		if adapter.Mode() != session.SecretModeMask {
			t.Errorf("Mode() = %q, want mask", adapter.Mode())
		}
	})
}

// TestGRPCPlugin_externalBinary builds and runs the example plugin as an
// external process to verify real-world usage.
func TestGRPCPlugin_externalBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping external binary test in short mode")
	}

	repoRoot := findRepoRoot(t)

	// Build the example plugin binary
	pluginBin := filepath.Join(t.TempDir(), "example-grpc-scanner")
	buildCmd := exec.Command("go", "build", "-o", pluginBin, "./examples/plugins/grpc/")
	buildCmd.Dir = repoRoot
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("building gRPC plugin: %v", err)
	}

	// Load via the full LoadGRPCPlugin flow
	adapter, err := LoadGRPCPlugin(pluginBin, session.SecretModeMask)
	if err != nil {
		t.Fatalf("LoadGRPCPlugin() error: %v", err)
	}
	defer adapter.Close()

	matches := adapter.Scan("found EXAMPLE_SECRET_ABCDEFGH in code")
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].Type != "EXAMPLE_SECRET" {
		t.Errorf("Type = %q, want EXAMPLE_SECRET", matches[0].Type)
	}

	masked := adapter.Mask("key=EXAMPLE_SECRET_ABCDEFGH")
	if !strings.Contains(masked, "***REDACTED:EXAMPLE_SECRET***") {
		t.Errorf("Mask() = %q, want redacted", masked)
	}
}

// TestNativePlugin_concept tests the NativeAdapter wrapping logic with a mock.
func TestNativePlugin_concept(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Go native plugins not supported on Windows")
	}

	mock := &mockDomainScanner{mode: session.SecretModeMask}
	adapter := &NativeAdapter{
		scanner: mock,
		mode:    session.SecretModeMask,
	}

	t.Run("scan delegates to wrapped scanner", func(t *testing.T) {
		matches := adapter.Scan("test content")
		if len(matches) != 1 {
			t.Fatalf("expected 1 match, got %d", len(matches))
		}
		if matches[0].Type != "MOCK_SECRET" {
			t.Errorf("Type = %q, want MOCK_SECRET", matches[0].Type)
		}
	})

	t.Run("mask delegates to wrapped scanner", func(t *testing.T) {
		masked := adapter.Mask("test content")
		if masked != "***MASKED***" {
			t.Errorf("Mask() = %q, want ***MASKED***", masked)
		}
	})

	t.Run("mode returns configured mode", func(t *testing.T) {
		if adapter.Mode() != session.SecretModeMask {
			t.Errorf("Mode() = %q, want mask", adapter.Mode())
		}
	})
}

// Compile-time interface checks
var (
	_ goplugin.Plugin     = (*GRPCPlugin)(nil)
	_ goplugin.GRPCPlugin = (*GRPCPlugin)(nil)
)

// --- Test helpers ---

// testScannerImpl is a simple scanner for in-process gRPC testing.
var testPattern = regexp.MustCompile(`TEST_SECRET_[A-Z]{8}`)

type testScannerImpl struct{}

func (s *testScannerImpl) Scan(content string) ([]*pb.SecretMatch, error) {
	locs := testPattern.FindAllStringIndex(content, -1)
	matches := make([]*pb.SecretMatch, 0, len(locs))
	for _, loc := range locs {
		matches = append(matches, &pb.SecretMatch{
			Type:     "TEST_SECRET",
			Value:    content[loc[0]:loc[1]],
			StartPos: int32(loc[0]),
			EndPos:   int32(loc[1]),
		})
	}
	return matches, nil
}

func (s *testScannerImpl) Mask(content string) (string, error) {
	locs := testPattern.FindAllStringIndex(content, -1)
	if len(locs) == 0 {
		return content, nil
	}
	var b strings.Builder
	prev := 0
	for _, loc := range locs {
		b.WriteString(content[prev:loc[0]])
		fmt.Fprint(&b, "***REDACTED:TEST_SECRET***")
		prev = loc[1]
	}
	b.WriteString(content[prev:])
	return b.String(), nil
}

type mockDomainScanner struct {
	mode session.SecretMode
}

func (m *mockDomainScanner) Scan(_ string) []session.SecretMatch {
	return []session.SecretMatch{{Type: "MOCK_SECRET", Value: "test"}}
}
func (m *mockDomainScanner) Mask(_ string) string     { return "***MASKED***" }
func (m *mockDomainScanner) Mode() session.SecretMode { return m.mode }

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (go.mod)")
		}
		dir = parent
	}
}
