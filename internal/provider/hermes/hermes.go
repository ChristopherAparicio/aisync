package hermes

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

const exportedByLabel = "aisync"

// Provider implements provider.Provider for the Hermes AI coding tool.
// It reads sessions from Hermes' SQLite database at ~/.hermes/state.db.
// The provider is read-only: CanImport returns false.
type Provider struct {
	hermesHome string
	reader     reader
}

// New creates a Hermes provider.
// If hermesHome is empty, it resolves from the HERMES_HOME environment variable,
// falling back to ~/.hermes.
func New(hermesHome string) *Provider {
	if hermesHome == "" {
		hermesHome = defaultHermesHome()
	}
	p := &Provider{hermesHome: hermesHome}
	if dbr, err := newDBReader(hermesHome); err == nil {
		p.reader = dbr
	}
	return p
}

// Name returns the provider identifier.
func (p *Provider) Name() session.ProviderName {
	return session.ProviderHermes
}

// Detect lists all top-level Hermes sessions.
// Hermes does not track projects natively, so all root sessions are returned
// regardless of projectPath; branch is ignored.
// Returns an empty list (no error) when state.db is not present.
func (p *Provider) Detect(_ string, _ string) ([]session.Summary, error) {
	if p.reader == nil {
		return nil, nil
	}

	sessions, err := p.reader.listSessions()
	if err != nil {
		return nil, fmt.Errorf("hermes: listing sessions: %w", err)
	}

	var summaries []session.Summary
	for _, hs := range sessions {
		// Skip child sessions — they are attached as children during Export.
		if hs.ParentSessionID.Valid {
			continue
		}

		s := mapSession(hs)
		summaries = append(summaries, session.Summary{
			ID:            s.ID,
			Provider:      session.ProviderHermes,
			Agent:         s.Agent,
			Summary:       s.Summary,
			MessageCount:  hs.MessageCount,
			ToolCallCount: hs.ToolCallCount,
			TotalTokens:   s.TokenUsage.TotalTokens,
			CreatedAt:     s.CreatedAt,
			EstimatedCost: s.EstimatedCost,
			ActualCost:    s.ActualCost,
		})
	}

	// Sort most recent first.
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].CreatedAt.After(summaries[j].CreatedAt)
	})

	return summaries, nil
}

// Export reads a session and converts it to the unified Session model.
func (p *Provider) Export(sessionID session.ID, mode session.StorageMode) (*session.Session, error) {
	if p.reader == nil {
		return nil, fmt.Errorf("hermes: state.db not available at %s", filepath.Join(p.hermesHome, dbFileName))
	}

	hs, err := p.reader.readSession(string(sessionID))
	if err != nil {
		return nil, err
	}

	result := mapSession(*hs)
	result.StorageMode = mode
	result.ExportedAt = time.Now()

	if mode != session.StorageModeSummary {
		msgs, msgErr := p.reader.listMessages(string(sessionID))
		if msgErr != nil {
			log.Printf("hermes: Export: listMessages for %s: %v", sessionID, msgErr)
		} else {
			for _, hm := range msgs {
				result.Messages = append(result.Messages, mapMessage(hm))
			}
		}
	}

	childSessions, childErr := p.loadChildSessions(string(sessionID), mode)
	if childErr != nil {
		log.Printf("hermes: Export: loadChildSessions for %s: %v", sessionID, childErr)
	} else if len(childSessions) > 0 {
		result.Children = childSessions
	}

	return &result, nil
}

// loadChildSessions recursively exports child sessions.
func (p *Provider) loadChildSessions(parentID string, mode session.StorageMode) ([]session.Session, error) {
	childSessions, err := p.reader.findChildSessions(parentID)
	if err != nil {
		return nil, err
	}

	var children []session.Session
	for _, cs := range childSessions {
		child, exportErr := p.Export(session.ID(cs.ID), mode)
		if exportErr != nil {
			continue
		}
		child.ParentID = session.ID(parentID)
		children = append(children, *child)
	}
	return children, nil
}

// CanImport reports that Hermes does not support session import.
// Hermes' state.db is managed exclusively by the Hermes process.
func (p *Provider) CanImport() bool {
	return false
}

// Import always returns an error because Hermes is read-only.
func (p *Provider) Import(_ *session.Session) error {
	return fmt.Errorf("hermes: import not supported — provider is read-only")
}

// mapSession converts a hermesSession row to the unified session.Session model.
// Messages are not mapped here; all token/cost/lineage fields are fully populated.
func mapSession(hs hermesSession) session.Session {
	agent := hs.Model
	if agent == "" {
		agent = hs.Source
	}

	var parentID session.ID
	if hs.ParentSessionID.Valid {
		parentID = session.ID(hs.ParentSessionID.String)
	}

	// StartedAt is stored as float64 epoch seconds in Hermes (not milliseconds).
	createdAt := time.Unix(int64(hs.StartedAt), 0)

	var title string
	if hs.Title.Valid {
		title = hs.Title.String
	}

	totalTokens := hs.InputTokens + hs.OutputTokens +
		hs.CacheReadTokens + hs.CacheWriteTokens + hs.ReasoningTokens

	var estimatedCost float64
	if hs.EstimatedCostUSD.Valid {
		estimatedCost = hs.EstimatedCostUSD.Float64
	}

	var actualCost float64
	if hs.ActualCostUSD.Valid {
		actualCost = hs.ActualCostUSD.Float64
	}

	return session.Session{
		ID:          session.ID(hs.ID),
		Provider:    session.ProviderHermes,
		Agent:       agent,
		ParentID:    parentID,
		CreatedAt:   createdAt,
		Summary:     title,
		ExportedBy:  exportedByLabel,
		StorageMode: session.StorageModeCompact,
		Version:     1,
		TokenUsage: session.TokenUsage{
			InputTokens:  hs.InputTokens,
			OutputTokens: hs.OutputTokens,
			CacheRead:    hs.CacheReadTokens,
			CacheWrite:   hs.CacheWriteTokens,
			TotalTokens:  totalTokens,
		},
		EstimatedCost: estimatedCost,
		ActualCost:    actualCost,
	}
}

type hermesToolCallJSON struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// mapMessage converts a hermesMessage row to the unified session.Message model.
func mapMessage(hm hermesMessage) session.Message {
	var content string
	if hm.Content.Valid {
		content = stripSentinel(hm.Content.String)
	}

	var thinking string
	if hm.Reasoning.Valid && hm.Reasoning.String != "" {
		thinking = hm.Reasoning.String
	} else if hm.ReasoningContent.Valid {
		thinking = hm.ReasoningContent.String
	}

	msg := session.Message{
		ID:        hm.ID,
		Role:      session.MessageRole(hm.Role),
		Content:   content,
		Thinking:  thinking,
		Timestamp: time.Unix(int64(hm.Timestamp), 0),
	}

	if hm.Role == "assistant" {
		msg.OutputTokens = hm.TokenCount
	}

	if hm.ToolCalls.Valid && hm.ToolCalls.String != "" {
		var raw []hermesToolCallJSON
		if err := json.Unmarshal([]byte(hm.ToolCalls.String), &raw); err == nil {
			for _, tc := range raw {
				inputStr := string(tc.Input)
				if inputStr == "null" {
					inputStr = ""
				}
				msg.ToolCalls = append(msg.ToolCalls, session.ToolCall{
					ID:    tc.ID,
					Name:  tc.Name,
					Input: inputStr,
					State: session.ToolStateCompleted,
				})
			}
		}
	}

	return msg
}

// defaultHermesHome resolves the Hermes home directory from the environment,
// falling back to ~/.hermes.
func defaultHermesHome() string {
	if h := os.Getenv("HERMES_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".hermes")
}
