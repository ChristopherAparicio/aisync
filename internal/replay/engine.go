package replay

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// Engine orchestrates the full replay flow:
// load session → create worktree → run agent → capture → compare → cleanup.
type Engine struct {
	store    storage.Store
	runners  map[session.ProviderName]Runner
	capturer Capturer // optional — nil skips replay session capture
	logger   *log.Logger
}

// EngineConfig holds dependencies for creating an Engine.
type EngineConfig struct {
	Store    storage.Store
	Runners  []Runner // available runners (OpenCode, Claude Code)
	Capturer Capturer // optional — captures the replay session from the worktree
	Logger   *log.Logger
}

// NewEngine creates a replay engine.
func NewEngine(cfg EngineConfig) *Engine {
	runners := make(map[session.ProviderName]Runner)
	for _, r := range cfg.Runners {
		runners[session.ProviderName(r.Name())] = r
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &Engine{
		store:    cfg.Store,
		runners:  runners,
		capturer: cfg.Capturer,
		logger:   logger,
	}
}

// Replay executes a full session replay.
func (e *Engine) Replay(ctx context.Context, req Request) (*Result, error) {
	start := time.Now()

	// 1. Load the original session.
	original, err := e.store.Get(req.SourceSessionID)
	if err != nil {
		return nil, fmt.Errorf("loading source session: %w", err)
	}

	result := &Result{
		OriginalSession: original,
	}

	// Determine provider and agent.
	provider := req.Provider
	if provider == "" {
		provider = original.Provider
	}
	agent := req.Agent
	if agent == "" {
		agent = original.Agent
	}
	model := req.Model
	commitSHA := req.CommitSHA
	if commitSHA == "" {
		commitSHA = original.CommitSHA
	}

	// Find the runner.
	runner, ok := e.runners[provider]
	if !ok {
		return nil, fmt.Errorf("no runner available for provider %q (available: %v)", provider, e.runnerNames())
	}

	// Extract user messages to replay.
	userMessages := extractUserMessages(original.Messages, req.MaxMessages)
	if len(userMessages) == 0 {
		return nil, fmt.Errorf("no user messages to replay in session %s", req.SourceSessionID)
	}

	e.logger.Printf("[replay] replaying session %s (%d user messages) with %s/%s",
		req.SourceSessionID, len(userMessages), provider, agent)

	// 2. Create a git worktree.
	projectPath := original.ProjectPath
	if projectPath == "" {
		return nil, fmt.Errorf("session %s has no project_path — cannot create worktree", req.SourceSessionID)
	}

	wt, wtErr := CreateWorktree(projectPath, commitSHA)
	if wtErr != nil {
		return nil, fmt.Errorf("creating worktree: %w", wtErr)
	}
	defer func() {
		if removeErr := wt.Remove(); removeErr != nil {
			e.logger.Printf("[replay] warning: failed to remove worktree %s: %v", wt.Path(), removeErr)
		}
	}()

	result.WorktreePath = wt.Path()
	e.logger.Printf("[replay] worktree created at %s (commit: %s)", wt.Path(), commitSHA)

	// 3. Run each user message sequentially.
	opts := RunOptions{
		Agent: agent,
		Model: model,
	}

	for i, msg := range userMessages {
		e.logger.Printf("[replay] sending message %d/%d: %.80s", i+1, len(userMessages), msg)

		select {
		case <-ctx.Done():
			result.Error = ctx.Err().Error()
			result.Duration = time.Since(start)
			return result, ctx.Err()
		default:
		}

		_, runErr := runner.Run(ctx, wt.Path(), msg, opts)
		if runErr != nil {
			e.logger.Printf("[replay] message %d failed: %v (continuing)", i+1, runErr)
			// Don't abort — try remaining messages.
		}
	}

	// 4. Capture the replay session from the worktree.
	e.logger.Printf("[replay] agent execution complete, capturing replay session...")

	if e.capturer != nil {
		replaySess, captureErr := e.capturer.CaptureReplay(provider, wt.Path(), req.SourceSessionID)
		if captureErr != nil {
			e.logger.Printf("[replay] capture failed: %v (replay ran but session not captured)", captureErr)
			result.Error = fmt.Sprintf("agent ran but capture failed: %v", captureErr)
		} else {
			result.ReplaySession = replaySess
			e.logger.Printf("[replay] captured replay session %s", replaySess.ID)
		}
	} else {
		e.logger.Printf("[replay] no capturer configured — skipping session capture")
	}

	result.Duration = time.Since(start)

	// 5. If we have both sessions, compare them.
	if result.ReplaySession != nil {
		result.Comparison = Compare(original, result.ReplaySession)
		e.logger.Printf("[replay] verdict: %s (tokens: %d→%d, errors: %d→%d)",
			result.Comparison.Verdict,
			result.Comparison.OriginalTokens, result.Comparison.ReplayTokens,
			result.Comparison.OriginalErrors, result.Comparison.ReplayErrors,
		)
	}

	e.logger.Printf("[replay] completed in %s", result.Duration.Round(time.Millisecond))
	return result, nil
}

// extractUserMessages returns the content of user messages from a session.
func extractUserMessages(messages []session.Message, maxMessages int) []string {
	var result []string
	for i := range messages {
		if messages[i].Role != session.RoleUser {
			continue
		}
		content := messages[i].Content
		if content == "" {
			continue
		}
		result = append(result, content)
		if maxMessages > 0 && len(result) >= maxMessages {
			break
		}
	}
	return result
}

// runnerNames returns the names of available runners (for error messages).
func (e *Engine) runnerNames() []string {
	names := make([]string, 0, len(e.runners))
	for name := range e.runners {
		names = append(names, string(name))
	}
	return names
}
