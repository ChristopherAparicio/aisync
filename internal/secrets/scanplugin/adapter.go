package scanplugin

import (
	"fmt"
	"os/exec"

	goplugin "github.com/hashicorp/go-plugin"

	"github.com/ChristopherAparicio/aisync/internal/domain"
	pb "github.com/ChristopherAparicio/aisync/internal/secrets/scanplugin/proto"
)

// GRPCAdapter wraps a HashiCorp go-plugin scanner into a domain.SecretScanner.
type GRPCAdapter struct {
	client *goplugin.Client
	raw    ScannerPlugin
	mode   domain.SecretMode
}

// LoadGRPCPlugin loads an external scanner plugin from a binary path.
// The plugin binary must implement the aisync scanner gRPC service.
// The returned adapter must be closed when done (call Close()).
func LoadGRPCPlugin(binaryPath string, mode domain.SecretMode) (*GRPCAdapter, error) {
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

// Scan implements domain.SecretScanner.
func (a *GRPCAdapter) Scan(content string) []domain.SecretMatch {
	pbMatches, err := a.raw.Scan(content)
	if err != nil {
		return nil // plugin errors are silently ignored in scanning
	}
	return pbToDomain(pbMatches)
}

// Mask implements domain.SecretScanner.
func (a *GRPCAdapter) Mask(content string) string {
	masked, err := a.raw.Mask(content)
	if err != nil {
		return content // on error, return original content
	}
	return masked
}

// Mode implements domain.SecretScanner.
func (a *GRPCAdapter) Mode() domain.SecretMode {
	return a.mode
}

// Close terminates the plugin process.
func (a *GRPCAdapter) Close() {
	if a.client != nil {
		a.client.Kill()
	}
}

// pbToDomain converts protobuf matches to domain matches.
func pbToDomain(pbMatches []*pb.SecretMatch) []domain.SecretMatch {
	matches := make([]domain.SecretMatch, 0, len(pbMatches))
	for _, m := range pbMatches {
		matches = append(matches, domain.SecretMatch{
			Type:     m.Type,
			Value:    m.Value,
			StartPos: int(m.StartPos),
			EndPos:   int(m.EndPos),
		})
	}
	return matches
}
