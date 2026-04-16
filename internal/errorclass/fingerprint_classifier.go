// Package errorclass — fingerprint_classifier.go uses previously classified
// fingerprints to auto-classify new errors.
//
// When the user classifies a group of errors via the unclassified errors
// dashboard, the fingerprint + category pair is stored in the error_fingerprints
// table. This classifier checks whether the current error's fingerprint matches
// a previously classified group, and if so, applies the same classification.
//
// This provides zero-cost auto-classification for recurring errors: the user
// classifies once, and all future occurrences with the same fingerprint are
// classified automatically.
//
// Position in the classifier chain:
//
//	DeterministicClassifier → FingerprintClassifier → (optional) LLMClassifier
//
// The FingerprintClassifier sits between deterministic and LLM because:
//   - Deterministic rules are authoritative and free — always run first.
//   - Fingerprint matches are user-supplied and "high" confidence — cheaper than LLM.
//   - LLM is the last resort for truly novel errors.
package errorclass

import (
	"log/slog"

	"github.com/ChristopherAparicio/aisync/internal/session"
	"github.com/ChristopherAparicio/aisync/internal/storage"
)

// FingerprintClassifier looks up a previously classified fingerprint to
// auto-classify new errors. Implements session.ErrorClassifier.
type FingerprintClassifier struct {
	store  storage.ErrorStore
	logger *slog.Logger
}

// FingerprintClassifierConfig holds dependencies for creating a FingerprintClassifier.
type FingerprintClassifierConfig struct {
	Store  storage.ErrorStore // required — where to look up classified fingerprints
	Logger *slog.Logger       // optional — defaults to slog.Default()
}

// NewFingerprintClassifier creates a FingerprintClassifier with the given dependencies.
func NewFingerprintClassifier(cfg FingerprintClassifierConfig) *FingerprintClassifier {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &FingerprintClassifier{
		store:  cfg.Store,
		logger: logger,
	}
}

// Name returns the classifier identifier.
func (c *FingerprintClassifier) Name() string {
	return "fingerprint"
}

// Classify checks whether the error's fingerprint has been previously classified.
// If a match is found, it applies the stored category, message, and source.
// If not found, returns the error unchanged with category "unknown".
func (c *FingerprintClassifier) Classify(err session.SessionError) session.SessionError {
	if c.store == nil || err.Fingerprint == "" {
		err.Category = session.ErrorCategoryUnknown
		return err
	}

	match, lookupErr := c.store.GetFingerprintMatch(err.Fingerprint)
	if lookupErr != nil {
		c.logger.Debug("fingerprint lookup failed",
			"error_id", err.ID,
			"fingerprint", err.Fingerprint,
			"lookup_error", lookupErr,
		)
		err.Category = session.ErrorCategoryUnknown
		return err
	}
	if match == nil {
		err.Category = session.ErrorCategoryUnknown
		return err
	}

	// Apply the classification from the previously classified group.
	err.Category = match.Category
	err.Message = match.Message
	err.Confidence = "high"

	// Infer source from category.
	switch match.Category {
	case session.ErrorCategoryProviderError, session.ErrorCategoryRateLimit,
		session.ErrorCategoryContextOverflow, session.ErrorCategoryAuthError,
		session.ErrorCategoryNetworkError:
		err.Source = session.ErrorSourceProvider
	case session.ErrorCategoryToolError:
		err.Source = session.ErrorSourceTool
	case session.ErrorCategoryAborted:
		err.Source = session.ErrorSourceClient
	default:
		if err.Source == "" {
			err.Source = session.ErrorSourceClient
		}
	}

	return err
}
