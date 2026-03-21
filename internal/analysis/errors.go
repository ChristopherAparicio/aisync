package analysis

import "errors"

// Sentinel errors for expected failures.
var (
	// ErrAnalysisNotFound is returned when an analysis lookup yields no results.
	ErrAnalysisNotFound = errors.New("analysis not found")
)
