// Package scanplugin provides plugin infrastructure for secret scanner extensions.
// It supports two plugin mechanisms:
//   - Go native plugins (plugin.Open, .so files, Linux/macOS only)
//   - HashiCorp go-plugin (gRPC, cross-platform, external process)
//
// Both mechanisms expose scanners that implement domain.SecretScanner.
package scanplugin

import (
	goplugin "github.com/hashicorp/go-plugin"

	pb "github.com/ChristopherAparicio/aisync/internal/secrets/scanplugin/proto"
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
// This is a simplified version of domain.SecretScanner designed for cross-process use.
type ScannerPlugin interface {
	Scan(content string) ([]*pb.SecretMatch, error)
	Mask(content string) (string, error)
}
