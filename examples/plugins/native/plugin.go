// This is an example Go native plugin scanner for aisync.
// It detects a simple pattern "NATIVE_KEY_[0-9]{6}" as a demonstration.
//
// Build (must use -buildmode=plugin):
//
//	go build -buildmode=plugin -o example-native-scanner.so ./examples/plugins/native/
//
// Usage in aisync config:
//
//	aisync config set secrets.plugin.native "/path/to/example-native-scanner.so"
//
// NOTE: Go native plugins only work on Linux and macOS.
// The plugin and the host binary MUST be built with the same Go version.
package main

import (
	"fmt"
	"regexp"
	"strings"
)

// nativePattern detects "NATIVE_KEY_" followed by 6 digits.
var nativePattern = regexp.MustCompile(`NATIVE_KEY_[0-9]{6}`)

// SecretMatch mirrors session.SecretMatch so the plugin doesn't import internal packages.
type SecretMatch struct {
	Type     string
	Value    string
	StartPos int
	EndPos   int
}

// nativeScanner implements the scanner interface.
type nativeScanner struct {
	mode string
}

func (s *nativeScanner) Scan(content string) []SecretMatch {
	locs := nativePattern.FindAllStringIndex(content, -1)
	matches := make([]SecretMatch, 0, len(locs))
	for _, loc := range locs {
		matches = append(matches, SecretMatch{
			Type:     "NATIVE_KEY",
			Value:    content[loc[0]:loc[1]],
			StartPos: loc[0],
			EndPos:   loc[1],
		})
	}
	return matches
}

func (s *nativeScanner) Mask(content string) string {
	locs := nativePattern.FindAllStringIndex(content, -1)
	if len(locs) == 0 {
		return content
	}

	var b strings.Builder
	prev := 0
	for _, loc := range locs {
		b.WriteString(content[prev:loc[0]])
		fmt.Fprint(&b, "***REDACTED:NATIVE_KEY***")
		prev = loc[1]
	}
	b.WriteString(content[prev:])
	return b.String()
}

func (s *nativeScanner) Mode() string {
	return s.mode
}

// NewScanner is the exported symbol that aisync looks for.
// It returns an interface{} because Go native plugins can't share types across boundaries.
//
//nolint:unused
var NewScanner = func(mode string) interface{} {
	return &nativeScanner{mode: mode}
}

// main is required by Go for package main but unused in plugin mode.
// Build with: go build -buildmode=plugin -o example-native-scanner.so ./examples/plugins/native/
func main() {}
