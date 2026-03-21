package tuicmd

import (
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

func testFactory() *cmdutil.Factory {
	return &cmdutil.Factory{IOStreams: iostreams.Test()}
}

func TestNewCmdTUI_use(t *testing.T) {
	cmd := NewCmdTUI(testFactory())
	if cmd.Use != "tui" {
		t.Fatalf("expected Use %q, got %q", "tui", cmd.Use)
	}
}

func TestNewCmdTUI_short(t *testing.T) {
	cmd := NewCmdTUI(testFactory())
	if !strings.Contains(strings.ToLower(cmd.Short), "interactive") {
		t.Fatalf("expected Short to contain %q, got %q", "interactive", cmd.Short)
	}
}
