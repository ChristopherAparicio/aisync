// This is an example HashiCorp go-plugin scanner for aisync.
// It detects a simple pattern "EXAMPLE_SECRET_[A-Z]{8}" as a demonstration.
//
// Build:
//
//	go build -o example-grpc-scanner ./examples/plugins/grpc/
//
// Usage in aisync config:
//
//	aisync config set secrets.plugin.grpc "/path/to/example-grpc-scanner"
package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/go-plugin"

	sp "github.com/ChristopherAparicio/aisync/internal/secrets/scanplugin"
	pb "github.com/ChristopherAparicio/aisync/internal/secrets/scanplugin/proto"
)

// examplePattern detects "EXAMPLE_SECRET_" followed by 8 uppercase letters.
var examplePattern = regexp.MustCompile(`EXAMPLE_SECRET_[A-Z]{8}`)

// ExampleScanner is a simple scanner plugin implementation.
type ExampleScanner struct{}

// Scan detects example secrets.
func (s *ExampleScanner) Scan(content string) ([]*pb.SecretMatch, error) {
	locs := examplePattern.FindAllStringIndex(content, -1)
	matches := make([]*pb.SecretMatch, 0, len(locs))
	for _, loc := range locs {
		matches = append(matches, &pb.SecretMatch{
			Type:     "EXAMPLE_SECRET",
			Value:    content[loc[0]:loc[1]],
			StartPos: int32(loc[0]),
			EndPos:   int32(loc[1]),
		})
	}
	return matches, nil
}

// Mask replaces example secrets with redacted placeholders.
func (s *ExampleScanner) Mask(content string) (string, error) {
	locs := examplePattern.FindAllStringIndex(content, -1)
	if len(locs) == 0 {
		return content, nil
	}

	var b strings.Builder
	prev := 0
	for _, loc := range locs {
		b.WriteString(content[prev:loc[0]])
		fmt.Fprint(&b, "***REDACTED:EXAMPLE_SECRET***")
		prev = loc[1]
	}
	b.WriteString(content[prev:])
	return b.String(), nil
}

func main() {
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: sp.HandshakeConfig,
		Plugins: map[string]plugin.Plugin{
			"scanner": &sp.GRPCPlugin{Impl: &ExampleScanner{}},
		},
		GRPCServer: plugin.DefaultGRPCServer,
	})
}
