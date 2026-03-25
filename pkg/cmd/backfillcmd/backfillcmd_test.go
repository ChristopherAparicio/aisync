package backfillcmd_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/service"
	"github.com/ChristopherAparicio/aisync/internal/testutil"
	"github.com/ChristopherAparicio/aisync/pkg/cmd/backfillcmd"
	"github.com/ChristopherAparicio/aisync/pkg/cmdutil"
	"github.com/ChristopherAparicio/aisync/pkg/iostreams"
)

func newTestFactory(t *testing.T) (*cmdutil.Factory, *bytes.Buffer) {
	t.Helper()
	store := testutil.NewMockStore()
	svc := service.NewSessionService(service.SessionServiceConfig{
		Store: store,
	})
	out := &bytes.Buffer{}
	return &cmdutil.Factory{
		IOStreams: &iostreams.IOStreams{Out: out},
		SessionServiceFunc: func() (service.SessionServicer, error) {
			return svc, nil
		},
	}, out
}

func TestBackfillRemoteURL_Runs(t *testing.T) {
	f, out := newTestFactory(t)
	cmd := backfillcmd.NewCmdBackfill(f)
	cmd.SetArgs([]string{"remote-url"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "Backfill complete") {
		t.Errorf("expected 'Backfill complete' in output, got: %s", out.String())
	}
}

func TestBackfillRemoteURL_JSON(t *testing.T) {
	f, out := newTestFactory(t)
	cmd := backfillcmd.NewCmdBackfill(f)
	cmd.SetArgs([]string{"remote-url", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "Candidates") {
		t.Errorf("expected JSON with 'Candidates', got: %s", out.String())
	}
}

func TestBackfillForks_Runs(t *testing.T) {
	f, out := newTestFactory(t)
	cmd := backfillcmd.NewCmdBackfill(f)
	cmd.SetArgs([]string{"forks"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "Fork detection complete") {
		t.Errorf("expected 'Fork detection complete' in output, got: %s", out.String())
	}
}

func TestBackfillForks_JSON(t *testing.T) {
	f, out := newTestFactory(t)
	cmd := backfillcmd.NewCmdBackfill(f)
	cmd.SetArgs([]string{"forks", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "SessionsScanned") {
		t.Errorf("expected JSON with 'SessionsScanned', got: %s", out.String())
	}
}
