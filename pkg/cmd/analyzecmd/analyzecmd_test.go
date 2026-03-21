package analyzecmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

// ---------------------------------------------------------------------------
// NewCmdAnalyze — flags
// ---------------------------------------------------------------------------

func TestNewCmdAnalyze_flags(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}

	cmd := NewCmdAnalyze(f)

	if cmd.Flags().Lookup("json") == nil {
		t.Error("expected --json flag on analyze command")
	}
}

func TestNewCmdAnalyze_requiresExactlyOneArg(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}

	cmd := NewCmdAnalyze(f)

	// No args — should fail validation.
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for zero args, got nil")
	}
}

func TestNewCmdAnalyze_useLine(t *testing.T) {
	ios := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}

	cmd := NewCmdAnalyze(f)

	if !strings.Contains(cmd.Use, "session-id") {
		t.Errorf("expected Use to contain 'session-id', got: %q", cmd.Use)
	}
}

// ---------------------------------------------------------------------------
// runAnalyze — AnalysisService init error
// ---------------------------------------------------------------------------

func TestAnalyze_serviceError(t *testing.T) {
	ios := iostreams.Test()

	f := &cmdutil.Factory{
		IOStreams: ios,
		AnalysisServiceFunc: func() (service.AnalysisServicer, error) {
			return nil, errors.New("no adapter configured")
		},
	}

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "test-id",
	}

	err := runAnalyze(opts)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "initializing analysis service") {
		t.Errorf("expected 'initializing analysis service' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "no adapter configured") {
		t.Errorf("expected 'no adapter configured' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// runAnalyze — AnalysisService nil func (not configured)
// ---------------------------------------------------------------------------

func TestAnalyze_nilServiceFunc(t *testing.T) {
	ios := iostreams.Test()

	// Factory without AnalysisServiceFunc set at all.
	f := &cmdutil.Factory{
		IOStreams: ios,
	}

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "test-id",
	}

	err := runAnalyze(opts)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "initializing analysis service") {
		t.Errorf("expected 'initializing analysis service' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// runAnalyze — empty session ID (ParseID rejects "")
// ---------------------------------------------------------------------------

func TestAnalyze_emptySessionID(t *testing.T) {
	ios := iostreams.Test()

	// AnalysisService that succeeds, but session ID is empty.
	svc := service.NewAnalysisService(service.AnalysisServiceConfig{})

	f := &cmdutil.Factory{
		IOStreams: ios,
		AnalysisServiceFunc: func() (service.AnalysisServicer, error) {
			return svc, nil
		},
	}

	opts := &Options{
		IO:        ios,
		Factory:   f,
		SessionID: "",
	}

	err := runAnalyze(opts)
	if err == nil {
		t.Fatal("expected error for empty session ID, got nil")
	}
	if !strings.Contains(err.Error(), "session ID cannot be empty") {
		t.Errorf("expected 'session ID cannot be empty' in error, got: %v", err)
	}
}
