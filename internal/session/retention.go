package session

import "time"

// WindowRetentionPolicy configures warm-tier transcript windowing.
type WindowRetentionPolicy struct {
	MaxTokens        int
	KeepUserMessages bool
	KeepCompactions  bool
	KeepToolOutputs  bool
	KeepThinking     bool
}

// WindowRetentionStats reports what warm-tier compaction changed.
type WindowRetentionStats struct {
	OriginalMessages int
	RetainedMessages int
	OriginalTokens   int
	WindowTokens     int
}

// ApplyWindowRetention mutates a session into the warm windowed tier.
func ApplyWindowRetention(sess *Session, policy WindowRetentionPolicy, compactedAt time.Time) WindowRetentionStats {
	if sess == nil {
		return WindowRetentionStats{}
	}
	if policy.MaxTokens <= 0 {
		policy.MaxTokens = 300000
	}

	keep := make(map[int]bool, len(sess.Messages))
	inWindow := make(map[int]bool, len(sess.Messages))
	windowTokens := 0
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		msgTokens := MessageTokenCount(sess.Messages[i])
		if msgTokens == 0 || windowTokens+msgTokens <= policy.MaxTokens || windowTokens == 0 {
			keep[i] = true
			inWindow[i] = true
			windowTokens += msgTokens
			continue
		}
		break
	}

	if policy.KeepUserMessages {
		for i, msg := range sess.Messages {
			if msg.Role == RoleUser {
				keep[i] = true
			}
		}
	}
	if policy.KeepCompactions {
		for i, msg := range sess.Messages {
			if msg.IsCompactionSummary {
				keep[i] = true
			}
		}
	}

	retained := make([]Message, 0, len(keep))
	for i, msg := range sess.Messages {
		if !keep[i] {
			continue
		}
		retained = append(retained, trimRetainedMessage(msg, policy, inWindow[i]))
	}

	stats := WindowRetentionStats{
		OriginalMessages: len(sess.Messages),
		RetainedMessages: len(retained),
		OriginalTokens:   sess.TokenUsage.TotalTokens,
		WindowTokens:     windowTokens,
	}
	sess.Messages = retained
	sess.StorageMode = StorageModeCompact
	sess.RetentionTier = RetentionTierWarm
	sess.RetentionFidelity = RetentionFidelityWindowed
	sess.CompactedAt = compactedAt
	return stats
}

// MessageTokenCount returns the best available token count for one message.
func MessageTokenCount(msg Message) int {
	return msg.InputTokens + msg.OutputTokens + msg.CacheReadTokens + msg.CacheWriteTokens
}

func trimRetainedMessage(msg Message, policy WindowRetentionPolicy, inWindow bool) Message {
	if !policy.KeepThinking {
		msg.Thinking = ""
		msg.ContentBlocks = filterThinkingBlocks(msg.ContentBlocks)
	}
	if !policy.KeepToolOutputs && !inWindow {
		for i := range msg.ToolCalls {
			msg.ToolCalls[i].Output = ""
			msg.ToolCalls[i].OutputTokens = 0
		}
	}
	return msg
}

func filterThinkingBlocks(blocks []ContentBlock) []ContentBlock {
	if len(blocks) == 0 {
		return nil
	}
	filtered := make([]ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == ContentBlockThinking {
			continue
		}
		block.Thinking = ""
		filtered = append(filtered, block)
	}
	return filtered
}
