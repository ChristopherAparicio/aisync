// Package errorclass — composite.go implements the CompositeClassifier.
//
// CompositeClassifier chains multiple ErrorClassifier implementations:
// deterministic rules first (fast, free), then LLM fallback for errors
// that remain "unknown". This gives the best of both worlds — instant
// classification for well-known patterns, and intelligent classification
// for ambiguous or novel errors.
package errorclass

import (
	"log/slog"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// CompositeClassifier runs classifiers in priority order.
// The first classifier to produce a non-unknown result wins.
// Implements session.ErrorClassifier.
type CompositeClassifier struct {
	classifiers []session.ErrorClassifier
	logger      *slog.Logger
}

// CompositeClassifierConfig holds dependencies for creating a CompositeClassifier.
type CompositeClassifierConfig struct {
	Classifiers []session.ErrorClassifier // ordered list — first match wins
	Logger      *slog.Logger              // optional — defaults to slog.Default()
}

// NewCompositeClassifier creates a composite classifier from the given classifiers.
// Classifiers are tried in order; the first to return a non-unknown category wins.
func NewCompositeClassifier(cfg CompositeClassifierConfig) *CompositeClassifier {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &CompositeClassifier{
		classifiers: cfg.Classifiers,
		logger:      logger,
	}
}

// Name returns the classifier identifier.
func (c *CompositeClassifier) Name() string {
	return "composite"
}

// Classify runs each classifier in order until one produces a non-unknown result.
func (c *CompositeClassifier) Classify(err session.SessionError) session.SessionError {
	// Already classified? Don't override.
	if err.Category != "" && err.Category != session.ErrorCategoryUnknown {
		return err
	}

	for _, classifier := range c.classifiers {
		result := classifier.Classify(err)

		if result.Category != session.ErrorCategoryUnknown {
			c.logger.Debug("error classified",
				"error_id", err.ID,
				"classifier", classifier.Name(),
				"category", result.Category,
			)
			return result
		}
	}

	// All classifiers returned unknown — return the last result.
	c.logger.Debug("all classifiers returned unknown",
		"error_id", err.ID,
		"raw_error", truncate(err.RawError, 100),
	)

	// Ensure we return a well-formed unknown classification.
	err.Category = session.ErrorCategoryUnknown
	if err.Source == "" {
		err.Source = session.ErrorSourceClient
	}
	err.Confidence = "low"
	if err.Message == "" {
		err.Message = "Unclassified error"
	}
	return err
}

// truncate shortens a string to max length, appending "..." if truncated.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
