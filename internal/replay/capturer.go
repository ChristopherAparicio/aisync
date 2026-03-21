package replay

import (
	"fmt"
	"log"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/provider"
	"github.com/ChristopherAparicio/aisync/internal/provider/claude"
	"github.com/ChristopherAparicio/aisync/internal/provider/opencode"
	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// Capturer extracts the session created by an agent during replay.
// After the agent runs in a worktree, the capturer reads the session
// from the provider's native storage, saves it to the aisync store,
// and creates a replay_of link to the original session.
type Capturer interface {
	// CaptureReplay reads the most recent session from the provider's
	// native storage for the given worktree path, saves it, and links it
	// to the original session.
	CaptureReplay(providerName session.ProviderName, worktreePath string, originalID session.ID) (*session.Session, error)
}

// ProviderCapturer implements Capturer using aisync's existing providers.
// It creates temporary provider instances pointed at the worktree path.
type ProviderCapturer struct {
	store  storage.Store
	logger *log.Logger
}

// NewProviderCapturer creates a capturer that uses provider.Detect/Export.
func NewProviderCapturer(store storage.Store, logger *log.Logger) *ProviderCapturer {
	if logger == nil {
		logger = log.Default()
	}
	return &ProviderCapturer{store: store, logger: logger}
}

// CaptureReplay detects the most recent session in the worktree,
// exports it, saves it to the store, and creates a replay_of link.
func (c *ProviderCapturer) CaptureReplay(providerName session.ProviderName, worktreePath string, originalID session.ID) (*session.Session, error) {
	// Create a provider instance — the default data dirs are fine because
	// both OpenCode and Claude store sessions globally (not per-project).
	// The provider will match sessions by worktree path.
	prov, err := c.providerFor(providerName)
	if err != nil {
		return nil, fmt.Errorf("creating provider for capture: %w", err)
	}

	// Detect sessions for the worktree path.
	summaries, err := prov.Detect(worktreePath, "")
	if err != nil {
		return nil, fmt.Errorf("detecting replay sessions in %s: %w", worktreePath, err)
	}
	if len(summaries) == 0 {
		return nil, fmt.Errorf("no sessions found in worktree %s for provider %s", worktreePath, providerName)
	}

	// Take the most recent session (summaries are sorted newest-first).
	latest := summaries[0]
	c.logger.Printf("[replay-capture] found session %s in worktree (%s)", latest.ID, providerName)

	// Export the full session.
	sess, err := prov.Export(latest.ID, session.StorageModeFull)
	if err != nil {
		return nil, fmt.Errorf("exporting replay session %s: %w", latest.ID, err)
	}

	// Tag the session as a replay.
	sess.SessionType = "replay"

	// Save to the aisync store.
	if saveErr := c.store.Save(sess); saveErr != nil {
		return nil, fmt.Errorf("saving replay session: %w", saveErr)
	}
	c.logger.Printf("[replay-capture] saved replay session %s (%d messages, %d tokens)",
		sess.ID, len(sess.Messages), sess.TokenUsage.TotalTokens)

	// Create a replay_of link: replay → original.
	link := session.SessionLink{
		ID:              session.NewID(),
		SourceSessionID: sess.ID,
		TargetSessionID: originalID,
		LinkType:        session.SessionLinkReplayOf,
		Description:     fmt.Sprintf("replay of session %s", originalID),
		CreatedAt:       time.Now().UTC(),
	}
	if linkErr := c.store.LinkSessions(link); linkErr != nil {
		c.logger.Printf("[replay-capture] warning: failed to create replay_of link: %v", linkErr)
		// Non-fatal — the session is still captured.
	}

	return sess, nil
}

// providerFor creates a provider instance for the given name.
// Uses default data directories since both providers store data globally.
func (c *ProviderCapturer) providerFor(name session.ProviderName) (provider.Provider, error) {
	switch name {
	case session.ProviderOpenCode:
		return opencode.New(""), nil
	case session.ProviderClaudeCode:
		return claude.New(""), nil
	default:
		return nil, fmt.Errorf("replay capture not supported for provider %q", name)
	}
}
