package service

import (
	"context"
	"fmt"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// SessionSaturationCurve computes the per-message context saturation curve for a session.
func (s *SessionService) SessionSaturationCurve(ctx context.Context, sessID session.ID) (*session.SaturationCurve, error) {
	sess, err := s.store.Get(sessID)
	if err != nil {
		return nil, fmt.Errorf("getting session: %w", err)
	}
	if len(sess.Messages) == 0 {
		return nil, fmt.Errorf("session has no messages")
	}

	// Find dominant model and its context window.
	modelCounts := make(map[string]int)
	for _, msg := range sess.Messages {
		if msg.Model != "" && msg.Role == session.RoleAssistant {
			modelCounts[msg.Model]++
		}
	}
	var dominantModel string
	var maxCount int
	for model, count := range modelCounts {
		if count > maxCount {
			dominantModel = model
			maxCount = count
		}
	}

	var maxInputTokens int
	if s.pricing != nil && dominantModel != "" {
		if mp, ok := s.pricing.Lookup(dominantModel); ok {
			maxInputTokens = mp.MaxInputTokens
		}
	}
	if maxInputTokens == 0 {
		maxInputTokens = 200000 // fallback default
	}

	// Zone thresholds.
	degradedThreshold := float64(maxInputTokens) * 0.40
	criticalThreshold := float64(maxInputTokens) * 0.80

	curve := &session.SaturationCurve{
		SessionID:      sess.ID,
		Model:          dominantModel,
		MaxInputTokens: maxInputTokens,
	}

	// Run compaction detection on the full message list.
	var inputRate float64
	if s.pricing != nil && dominantModel != "" {
		if mp, ok := s.pricing.Lookup(dominantModel); ok {
			inputRate = mp.InputPerMToken / 1_000_000
		}
	}
	compactions := session.DetectCompactions(sess.Messages, inputRate)

	// Build a set of compaction AfterMessageIdx for labeling.
	compactionAt := make(map[int]bool, len(compactions.Events))
	for _, e := range compactions.Events {
		compactionAt[e.AfterMessageIdx] = true
	}

	var prevInput int

	for i, msg := range sess.Messages {
		if msg.InputTokens == 0 && msg.Role != session.RoleUser {
			continue // skip messages without token data
		}

		inputTokens := msg.InputTokens
		if inputTokens == 0 {
			inputTokens = prevInput // user messages often don't have input_tokens
		}

		pct := float64(inputTokens) / float64(maxInputTokens) * 100
		if pct > 100 {
			pct = 100
		}

		// Determine zone.
		zone := "optimal"
		if float64(inputTokens) >= criticalThreshold {
			zone = "critical"
		} else if float64(inputTokens) >= degradedThreshold {
			zone = "degraded"
		}

		// Detect zone transitions.
		if curve.MsgAtDegraded == 0 && zone != "optimal" {
			curve.MsgAtDegraded = i + 1
		}
		if curve.MsgAtCritical == 0 && zone == "critical" {
			curve.MsgAtCritical = i + 1
		}

		// Delta from previous.
		delta := inputTokens - prevInput

		// Build label — annotate compaction events.
		label := labelForMessage(msg, delta)
		if compactionAt[i] {
			label = fmt.Sprintf("⚡ Compaction (%dK lost)", (prevInput-inputTokens)/1000)
		}

		point := session.SaturationPoint{
			MessageIndex: i,
			Role:         string(msg.Role),
			InputTokens:  inputTokens,
			Percent:      pct,
			Zone:         zone,
			Delta:        delta,
			Label:        label,
		}
		curve.Points = append(curve.Points, point)

		if inputTokens > curve.PeakTokens {
			curve.PeakTokens = inputTokens
			curve.PeakPercent = pct
		}
		prevInput = inputTokens
	}

	// Init overhead = first assistant message's input tokens (system prompt + context).
	for _, pt := range curve.Points {
		if pt.Role == "assistant" && pt.InputTokens > 0 {
			curve.InitOverhead = pt.InputTokens
			break
		}
	}

	curve.WasCompacted = compactions.TotalCompactions > 0
	curve.Compactions = compactions
	curve.Overload = session.DetectOverload(sess.Messages)
	return curve, nil
}

// labelForMessage creates a short description for a saturation point.
func labelForMessage(msg session.Message, delta int) string {
	prefix := ""
	switch msg.Role {
	case session.RoleUser:
		prefix = "User"
	case session.RoleAssistant:
		prefix = "Assistant"
	default:
		prefix = string(msg.Role)
	}

	// Check for large tool outputs.
	for _, tc := range msg.ToolCalls {
		outTokens := tc.OutputTokens
		if outTokens == 0 {
			outTokens = len(tc.Output) / 4 // rough estimate
		}
		if outTokens > 2000 {
			return fmt.Sprintf("%s (%s +%dK)", prefix, tc.Name, outTokens/1000)
		}
	}

	if delta > 5000 {
		return fmt.Sprintf("%s (+%dK)", prefix, delta/1000)
	}
	return prefix
}
