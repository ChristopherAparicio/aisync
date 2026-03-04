// Package scanplugin provides plugin infrastructure for secret scanner extensions.
// It supports two plugin mechanisms:
//   - Go native plugins (plugin.Open, .so files, Linux/macOS only)
//   - HashiCorp go-plugin (gRPC, cross-platform, external process)
//
// Both mechanisms expose scanners that implement the SecretScanner interface.
package scanplugin

import (
	goplugin "github.com/hashicorp/go-plugin"

	pb "github.com/ChristopherAparicio/aisync/internal/secrets/scanplugin/proto"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// HandshakeConfig is the shared handshake that both plugin host and plugin
// must agree on. This prevents incompatible plugins from loading.
var HandshakeConfig = goplugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "AISYNC_SCANNER_PLUGIN",
	MagicCookieValue: "aisync-secret-scanner-v1",
}

// PluginMap is the map of plugins we can load. The key "scanner" is what
// both host and plugin use to negotiate the implementation.
var PluginMap = map[string]goplugin.Plugin{
	"scanner": &GRPCPlugin{},
}

// ScannerPlugin is the interface that gRPC plugin implementations must satisfy.
// This is a simplified version of SecretScanner designed for cross-process use.
type ScannerPlugin interface {
	Scan(content string) ([]*pb.SecretMatch, error)
	Mask(content string) (string, error)
}

// SecretScanner detects and handles secrets in session content.
// This is the local interface used by native Go plugins (.so) and adapters
// within the scanplugin package.
type SecretScanner interface {
	// Scan checks content for secrets and returns all matches found.
	Scan(content string) []session.SecretMatch

	// Mask replaces detected secrets with redacted placeholders.
	Mask(content string) string

	// Mode returns the current secret handling mode.
	Mode() session.SecretMode
}
