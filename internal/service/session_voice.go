package service

import (
	"fmt"
	"time"
)

// ── Voice helpers ──

// sanitizeForVoice strips markdown, code blocks, and excessive whitespace
// from a summary, truncating to at most 2 sentences for TTS consumption.
func sanitizeForVoice(s string) string {
	if s == "" {
		return ""
	}

	// Strip common markdown: bold, italic, inline code, links.
	result := s
	result = stripPattern(result, "```", "```") // fenced code blocks
	result = stripInline(result, "**")          // bold
	result = stripInline(result, "__")          // bold
	result = stripInline(result, "*")           // italic
	result = stripInline(result, "_")           // italic
	result = stripInline(result, "`")           // inline code

	// Collapse whitespace.
	result = collapseWhitespace(result)

	// Truncate to 2 sentences.
	result = truncateSentences(result, 2)

	return result
}

// stripPattern removes everything between (and including) start/end markers.
func stripPattern(s, start, end string) string {
	for {
		i := indexOf(s, start)
		if i < 0 {
			break
		}
		j := indexOf(s[i+len(start):], end)
		if j < 0 {
			break
		}
		s = s[:i] + s[i+len(start)+j+len(end):]
	}
	return s
}

// stripInline removes paired inline markers (e.g. **bold** → bold).
func stripInline(s, marker string) string {
	for {
		i := indexOf(s, marker)
		if i < 0 {
			break
		}
		rest := s[i+len(marker):]
		j := indexOf(rest, marker)
		if j < 0 {
			break
		}
		s = s[:i] + rest[:j] + rest[j+len(marker):]
	}
	return s
}

// indexOf returns the index of sub in s, or -1 if not found.
func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// collapseWhitespace replaces runs of whitespace with a single space and trims.
func collapseWhitespace(s string) string {
	var b []byte
	space := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			if !space && len(b) > 0 {
				b = append(b, ' ')
			}
			space = true
		} else {
			b = append(b, c)
			space = false
		}
	}
	// Trim trailing space.
	if len(b) > 0 && b[len(b)-1] == ' ' {
		b = b[:len(b)-1]
	}
	return string(b)
}

// truncateSentences keeps at most n sentences. A sentence ends with '.', '!', or '?'.
func truncateSentences(s string, n int) string {
	count := 0
	for i, c := range s {
		if c == '.' || c == '!' || c == '?' {
			count++
			if count >= n {
				return s[:i+1]
			}
		}
	}
	return s
}

// humanTimeAgo returns a human-readable relative time string.
func humanTimeAgo(now, t time.Time) string {
	d := now.Sub(t)
	if d < 0 {
		d = -d
	}

	switch {
	case d < time.Minute:
		return "just now"
	case d < 2*time.Minute:
		return "1 minute ago"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 2*time.Hour:
		return "1 hour ago"
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	case d < 14*24*time.Hour:
		return "1 week ago"
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d weeks ago", int(d.Hours()/(24*7)))
	default:
		return t.Format("Jan 2, 2006")
	}
}
