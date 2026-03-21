package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ── JSON response helpers ──

// apiError is the standard error response body.
type apiError struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Details string `json:"details,omitempty"`
}

// writeJSON writes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{Error: msg})
}

// writeErrorWithCode writes a JSON error response with a machine-readable code.
func writeErrorWithCode(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, apiError{Error: msg, Code: code})
}

// mapServiceError maps known service/domain errors to HTTP status codes.
func mapServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrSessionNotFound):
		writeErrorWithCode(w, http.StatusNotFound, "session_not_found", err.Error())
	case errors.Is(err, session.ErrProviderNotDetected):
		writeErrorWithCode(w, http.StatusBadRequest, "provider_not_detected", err.Error())
	case errors.Is(err, session.ErrImportNotSupported):
		writeErrorWithCode(w, http.StatusBadRequest, "import_not_supported", err.Error())
	case errors.Is(err, session.ErrSecretDetected):
		writeErrorWithCode(w, http.StatusUnprocessableEntity, "secret_detected", err.Error())
	case errors.Is(err, session.ErrPRNotFound):
		writeErrorWithCode(w, http.StatusNotFound, "pr_not_found", err.Error())
	case errors.Is(err, session.ErrPlatformNotDetected):
		writeErrorWithCode(w, http.StatusBadRequest, "platform_not_detected", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// decodeJSON decodes JSON from the request body into v.
// Returns false and writes an error response if decoding fails.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, "request body is empty")
		return false
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

// ── Middleware ──

// withMiddleware wraps the mux with common middleware.
// Order: logging → auth → handler
func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return s.loggingMiddleware(s.authMiddleware(next))
}

// loggingMiddleware logs each request with method, path, status, and duration.
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		s.logger.Printf("%s %s %d %s", r.Method, r.URL.Path, rw.status, time.Since(start).Round(time.Millisecond))
	})
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}
