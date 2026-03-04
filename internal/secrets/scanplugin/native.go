package scanplugin

import (
	"fmt"
	"plugin"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// NativeAdapter wraps a Go native plugin (.so) into a SecretScanner.
// Go native plugins work on Linux and macOS only.
type NativeAdapter struct {
	scanner SecretScanner
	mode    session.SecretMode
}

// LoadNativePlugin loads a Go native plugin (.so file) that exports a
// "NewScanner" function returning a SecretScanner.
//
// The plugin must export:
//
//	var NewScanner func(mode string) interface{}
//
// The returned object must implement SecretScanner.
func LoadNativePlugin(soPath string, mode session.SecretMode) (*NativeAdapter, error) {
	p, err := plugin.Open(soPath)
	if err != nil {
		return nil, fmt.Errorf("opening native plugin %s: %w", soPath, err)
	}

	sym, err := p.Lookup("NewScanner")
	if err != nil {
		return nil, fmt.Errorf("plugin %s missing NewScanner symbol: %w", soPath, err)
	}

	// The plugin exports a function that creates a scanner.
	// We accept two function signatures for flexibility:
	//   1. func(mode string) SecretScanner
	//   2. func(mode string) interface{}
	switch fn := sym.(type) {
	case func(string) SecretScanner:
		return &NativeAdapter{
			scanner: fn(string(mode)),
			mode:    mode,
		}, nil
	case *func(string) interface{}:
		raw := (*fn)(string(mode))
		scanner, ok := raw.(SecretScanner)
		if !ok {
			return nil, fmt.Errorf("plugin %s: NewScanner did not return a SecretScanner", soPath)
		}
		return &NativeAdapter{
			scanner: scanner,
			mode:    mode,
		}, nil
	default:
		return nil, fmt.Errorf("plugin %s: NewScanner has unexpected type %T", soPath, sym)
	}
}

// Scan implements SecretScanner.
func (a *NativeAdapter) Scan(content string) []session.SecretMatch {
	return a.scanner.Scan(content)
}

// Mask implements SecretScanner.
func (a *NativeAdapter) Mask(content string) string {
	return a.scanner.Mask(content)
}

// Mode implements SecretScanner.
func (a *NativeAdapter) Mode() session.SecretMode {
	return a.mode
}
