package web

import (
	"testing"
	"time"
)

func TestTimeAgo(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero", time.Time{}, "—"},
		{"ancient", time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC), "—"},
		{"just_now", now.Add(-10 * time.Second), "just now"},
		{"1_min", now.Add(-90 * time.Second), "1 min ago"},
		{"5_mins", now.Add(-5 * time.Minute), "5 mins ago"},
		{"1_hour", now.Add(-90 * time.Minute), "1 hour ago"},
		{"3_hours", now.Add(-3 * time.Hour), "3 hours ago"},
		{"1_day", now.Add(-36 * time.Hour), "1 day ago"},
		{"7_days", now.Add(-7 * 24 * time.Hour), "7 days ago"},

		// Clock skew: slightly in the future should be "just now", not a date.
		{"future_5s", now.Add(5 * time.Second), "just now"},
		{"future_5min", now.Add(5 * time.Minute), "just now"},
		{"future_30min", now.Add(30 * time.Minute), "just now"},
		{"future_59min", now.Add(59 * time.Minute), "just now"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := timeAgo(tc.t)
			if got != tc.want {
				t.Errorf("timeAgo(%v) = %q, want %q", tc.t, got, tc.want)
			}
		})
	}

	// Future > 1 hour should show ISO date.
	farFuture := now.Add(2 * time.Hour)
	got := timeAgo(farFuture)
	if got != farFuture.Format("2006-01-02") {
		t.Errorf("timeAgo(far future) = %q, want date format", got)
	}
}
