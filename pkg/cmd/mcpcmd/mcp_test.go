package mcpcmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

func TestNewCmdMCP_use(t *testing.T) {
	f := &cmdutil.Factory{
		IOStreams: iostreams.Test(),
	}
	cmd := NewCmdMCP(f)

	if cmd.Use != "mcp" {
		t.Errorf("Use = %q, want %q", cmd.Use, "mcp")
	}
}

func TestMCP_serviceError(t *testing.T) {
	f := &cmdutil.Factory{
		IOStreams: iostreams.Test(),
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return nil, errors.New("not configured")
		},
	}
	cmd := NewCmdMCP(f)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "not configured")
	}
}

func TestMCP_nilServiceFunc(t *testing.T) {
	f := &cmdutil.Factory{
		IOStreams:          iostreams.Test(),
		SessionServiceFunc: nil,
	}
	cmd := NewCmdMCP(f)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, session.ErrConfigNotFound) {
		t.Errorf("error = %v, want %v", err, session.ErrConfigNotFound)
	}
}
