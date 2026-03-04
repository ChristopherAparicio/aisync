package scanplugin

import (
	"fmt"
	"os/exec"

	goplugin "github.com/hashicorp/go-plugin"

	pb "github.com/ChristopherAparicio/aisync/internal/secrets/scanplugin/proto"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// GRPCAdapter wraps a HashiCorp go-plugin scanner into a SecretScanner.
type GRPCAdapter struct {
	client *goplugin.Client
	raw    ScannerPlugin
	mode   session.SecretMode
}

// LoadGRPCPlugin loads an external scanner plugin from a binary path.
// The plugin binary must implement the aisync scanner gRPC service.
// The returned adapter must be closed when done (call Close()).
func LoadGRPCPlugin(binaryPath string, mode session.SecretMode) (*GRPCAdapter, error) {
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig: HandshakeConfig,
		Plugins:         PluginMap,
		Cmd:             exec.Command(binaryPath),
		AllowedProtocols: []goplugin.Protocol{
			goplugin.ProtocolGRPC,
		},
	})

	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("connecting to plugin %s: %w", binaryPath, err)
	}

	raw, err := rpcClient.Dispense("scanner")
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("dispensing scanner from plugin %s: %w", binaryPath, err)
	}

	scanner, ok := raw.(ScannerPlugin)
	if !ok {
		client.Kill()
		return nil, fmt.Errorf("plugin %s does not implement ScannerPlugin", binaryPath)
	}

	return &GRPCAdapter{
		client: client,
		raw:    scanner,
		mode:   mode,
	}, nil
}

// Scan implements SecretScanner.
func (a *GRPCAdapter) Scan(content string) []session.SecretMatch {
	pbMatches, err := a.raw.Scan(content)
	if err != nil {
		return nil // plugin errors are silently ignored in scanning
	}
	return pbToDomain(pbMatches)
}

// Mask implements SecretScanner.
func (a *GRPCAdapter) Mask(content string) string {
	masked, err := a.raw.Mask(content)
	if err != nil {
		return content // on error, return original content
	}
	return masked
}

// Mode implements SecretScanner.
func (a *GRPCAdapter) Mode() session.SecretMode {
	return a.mode
}

// Close terminates the plugin process.
func (a *GRPCAdapter) Close() {
	if a.client != nil {
		a.client.Kill()
	}
}

// pbToDomain converts protobuf matches to session matches.
func pbToDomain(pbMatches []*pb.SecretMatch) []session.SecretMatch {
	matches := make([]session.SecretMatch, 0, len(pbMatches))
	for _, m := range pbMatches {
		matches = append(matches, session.SecretMatch{
			Type:     m.Type,
			Value:    m.Value,
			StartPos: int(m.StartPos),
			EndPos:   int(m.EndPos),
		})
	}
	return matches
}
