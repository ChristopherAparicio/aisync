package webcmd

import (
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

func testFactory() *cmdutil.Factory {
	return &cmdutil.Factory{IOStreams: iostreams.Test()}
}

func TestNewCmdWeb_use(t *testing.T) {
	cmd := NewCmdWeb(testFactory())
	if cmd.Use != "web" {
		t.Fatalf("expected Use %q, got %q", "web", cmd.Use)
	}
}

func TestNewCmdWeb_addrFlag(t *testing.T) {
	cmd := NewCmdWeb(testFactory())
	fl := cmd.Flags().Lookup("addr")
	if fl == nil {
		t.Fatal("expected --addr flag to exist")
	}
	if fl.DefValue != "127.0.0.1:8371" {
		t.Fatalf("expected default %q, got %q", "127.0.0.1:8371", fl.DefValue)
	}
}

func TestNewCmdWeb_longDescription(t *testing.T) {
	cmd := NewCmdWeb(testFactory())
	if !strings.Contains(strings.ToLower(cmd.Long), "dashboard") {
		t.Fatalf("expected Long to contain %q, got %q", "dashboard", cmd.Long)
	}
}
