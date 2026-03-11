package web

import (
	"fmt"
	"html/template"
	"time"
)

// templateFuncs returns the FuncMap used by all web templates.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"formatTokens":   formatTokens,
		"formatCost":     formatCost,
		"formatDuration": formatDuration,
		"timeAgo":        timeAgo,
		"truncate":       truncate,
		"pct":            pct,
		"add":            func(a, b int) int { return a + b },
		"sub":            func(a, b int) int { return a - b },
	}
}

func formatTokens(n int) string {
	if n == 0 {
		return "0"
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func formatCost(cost float64) string {
	if cost == 0 {
		return "$0.00"
	}
	if cost < 0.01 {
		return fmt.Sprintf("$%.4f", cost)
	}
	return fmt.Sprintf("$%.2f", cost)
}

func timeAgo(t time.Time) string {
	if t.IsZero() || t.Year() < 2000 {
		return "—"
	}
	d := time.Since(t)
	if d < 0 {
		return t.Format("2006-01-02")
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 min ago"
		}
		return fmt.Sprintf("%d mins ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	case d < 30*24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	case d < 365*24*time.Hour:
		months := int(d.Hours() / 24 / 30)
		if months == 1 {
			return "1 month ago"
		}
		return fmt.Sprintf("%d months ago", months)
	default:
		return t.Format("Jan 2006")
	}
}

func truncate(v any, maxLen int) string {
	s := fmt.Sprint(v)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

func formatDuration(ms int) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	s := float64(ms) / 1000
	if s < 60 {
		return fmt.Sprintf("%.1fs", s)
	}
	m := int(s) / 60
	rem := int(s) % 60
	return fmt.Sprintf("%dm%ds", m, rem)
}

func pct(fraction float64) string {
	return fmt.Sprintf("%.1f%%", fraction)
}
