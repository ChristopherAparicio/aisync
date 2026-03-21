package service

import (
	"fmt"
	"strconv"

	restoresvc "github.com/ChristopherAparicio/aisync/internal/restore"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── Restore ──

// RestoreRequest contains inputs for a restore operation.
type RestoreRequest struct {
	ProjectPath  string
	Branch       string
	Agent        string
	SessionID    session.ID
	ProviderName session.ProviderName
	AsContext    bool
	PRNumber     int // if > 0, look up session linked to this PR
}

// RestoreResult contains the output of a restore operation.
type RestoreResult struct {
	Session     *session.Session
	Method      string // "native", "converted", or "context"
	ContextPath string
}

// Restore looks up a session and imports it into a target provider.
func (s *SessionService) Restore(req RestoreRequest) (*RestoreResult, error) {
	sessionID := req.SessionID

	// If --pr is set and no explicit session, look up by PR link
	if req.PRNumber > 0 && sessionID == "" {
		summaries, err := s.store.GetByLink(session.LinkPR, strconv.Itoa(req.PRNumber))
		if err != nil {
			return nil, fmt.Errorf("no session linked to PR #%d: %w", req.PRNumber, err)
		}
		if len(summaries) == 0 {
			return nil, fmt.Errorf("no session linked to PR #%d", req.PRNumber)
		}
		sessionID = summaries[0].ID
	}

	svc := restoresvc.NewServiceWithConverter(s.registry, s.store, s.converter)

	result, err := svc.Restore(restoresvc.Request{
		ProjectPath:  req.ProjectPath,
		Branch:       req.Branch,
		SessionID:    sessionID,
		ProviderName: req.ProviderName,
		Agent:        req.Agent,
		AsContext:    req.AsContext,
	})
	if err != nil {
		return nil, err
	}

	return &RestoreResult{
		Session:     result.Session,
		Method:      result.Method,
		ContextPath: result.ContextPath,
	}, nil
}
