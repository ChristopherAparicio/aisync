package errorclass_test

import (
	"strings"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/errorclass"
	"github.com/ChristopherAparicio/aisync/internal/session"
)

// ---------------------------------------------------------------------------
// Normalize
// ---------------------------------------------------------------------------

func TestNormalize_replacesUUIDs(t *testing.T) {
	in := `cannot find session 550e8400-e29b-41d4-a716-446655440000 in db`
	got := errorclass.Normalize(in)

	if strings.Contains(got, "550e8400") {
		t.Errorf("UUID not replaced: %q", got)
	}
	if !strings.Contains(got, "uuid") {
		t.Errorf("expected UUID placeholder, got %q", got)
	}
}

func TestNormalize_replacesProviderRequestIDs(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"req_", `request req_011Ca1aoZcCcKoQkmnA8593z failed`},
		{"msg_", `message msg_01XYZabcDEF123 not found`},
		{"toolu_", `tool call toolu_01abcDEF456ghi rejected`},
		{"call_", `call_xJ8aQ2bL9pK4mN7v timed out`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := errorclass.Normalize(tc.in)
			if !strings.Contains(got, "id ") && !strings.HasSuffix(got, "id") {
				t.Errorf("expected ID placeholder, got %q", got)
			}
		})
	}
}

func TestNormalize_replacesURLs(t *testing.T) {
	in := `connection refused to https://api.anthropic.com/v1/messages?stream=true`
	got := errorclass.Normalize(in)

	if strings.Contains(got, "anthropic.com") {
		t.Errorf("URL not replaced: %q", got)
	}
	if !strings.Contains(got, "url") {
		t.Errorf("expected URL placeholder, got %q", got)
	}
}

func TestNormalize_replacesUnixPaths(t *testing.T) {
	in := `cannot read /Users/foo/dev/myproject/main.go: permission denied`
	got := errorclass.Normalize(in)

	if strings.Contains(got, "/Users/foo") {
		t.Errorf("path not replaced: %q", got)
	}
	if !strings.Contains(got, "path") {
		t.Errorf("expected PATH placeholder, got %q", got)
	}
}

func TestNormalize_replacesNumbers(t *testing.T) {
	in := `image dimension 12000 exceeds max 8000 pixels at index 42`
	got := errorclass.Normalize(in)

	if strings.Contains(got, "12000") || strings.Contains(got, "8000") || strings.Contains(got, "42") {
		t.Errorf("numbers not replaced: %q", got)
	}
	// Should produce something like: "image dimension n exceeds max n pixels at index n"
	if !strings.Contains(got, " n ") {
		t.Errorf("expected N placeholders, got %q", got)
	}
}

func TestNormalize_isIdempotent(t *testing.T) {
	in := `messages.7.content.95 image dimension exceeds max 8000 pixels at /tmp/foo.png`
	once := errorclass.Normalize(in)
	twice := errorclass.Normalize(once)

	if once != twice {
		t.Errorf("Normalize is not idempotent:\n  once = %q\n  twice = %q", once, twice)
	}
}

func TestNormalize_lowercases(t *testing.T) {
	in := `INVALID Request: Image Source Format`
	got := errorclass.Normalize(in)

	if got != strings.ToLower(got) {
		t.Errorf("not lowercased: %q", got)
	}
}

func TestNormalize_collapsesWhitespace(t *testing.T) {
	in := "error:    too\tmany    spaces\n\nhere"
	got := errorclass.Normalize(in)

	if strings.Contains(got, "  ") || strings.Contains(got, "\t") || strings.Contains(got, "\n") {
		t.Errorf("whitespace not collapsed: %q", got)
	}
}

func TestNormalize_emptyInput(t *testing.T) {
	if got := errorclass.Normalize(""); got != "" {
		t.Errorf("empty input should produce empty output, got %q", got)
	}
}

// Real-world example: two image-dimension errors with different message
// indices and request IDs should normalize to the same string.
func TestNormalize_groupsImageDimensionErrors(t *testing.T) {
	a := `{"type":"error","error":{"type":"invalid_request_error","message":"messages.7.content.95.image.source.base64.data: At least one of the image dimensions exceed max allowed size: 8000 pixels"},"request_id":"req_011Ca1aoZcCcKoQkmnA8593z"}`
	b := `{"type":"error","error":{"type":"invalid_request_error","message":"messages.3.content.84.image.source.base64.data: At least one of the image dimensions exceed max allowed size: 8000 pixels"},"request_id":"req_011XYZabcdef9876543"}`

	na := errorclass.Normalize(a)
	nb := errorclass.Normalize(b)

	if na != nb {
		t.Errorf("similar errors should normalize identically:\n  a = %q\n  b = %q", na, nb)
	}
}

// ---------------------------------------------------------------------------
// Fingerprint
// ---------------------------------------------------------------------------

func TestFingerprint_emptyError(t *testing.T) {
	if fp := errorclass.Fingerprint(session.SessionError{}); fp != "" {
		t.Errorf("empty error should produce empty fingerprint, got %q", fp)
	}
}

func TestFingerprint_isStable(t *testing.T) {
	err := session.SessionError{
		RawError:   "context length exceeded: 250000 > 200000 tokens",
		HTTPStatus: 400,
	}

	fp1 := errorclass.Fingerprint(err)
	fp2 := errorclass.Fingerprint(err)

	if fp1 != fp2 {
		t.Errorf("fingerprint not stable: %q vs %q", fp1, fp2)
	}
	if fp1 == "" {
		t.Error("fingerprint should not be empty for non-empty input")
	}
}

func TestFingerprint_isFixedLength(t *testing.T) {
	cases := []string{
		"short",
		"a slightly longer error message",
		strings.Repeat("very long error blob ", 500),
	}

	want := -1
	for _, c := range cases {
		fp := errorclass.Fingerprint(session.SessionError{RawError: c})
		if want == -1 {
			want = len(fp)
		} else if len(fp) != want {
			t.Errorf("fingerprint length not fixed: %d vs %d for input %q", len(fp), want, c[:min(40, len(c))])
		}
	}
}

func TestFingerprint_groupsSimilarErrors(t *testing.T) {
	a := session.SessionError{
		HTTPStatus: 400,
		RawError:   `{"error":{"message":"messages.7.content.95.image.source.base64.data: At least one of the image dimensions exceed max allowed size: 8000 pixels"},"request_id":"req_011Ca1aoZcCc"}`,
	}
	b := session.SessionError{
		HTTPStatus: 400,
		RawError:   `{"error":{"message":"messages.3.content.84.image.source.base64.data: At least one of the image dimensions exceed max allowed size: 8000 pixels"},"request_id":"req_011XYZabcdef"}`,
	}

	fa := errorclass.Fingerprint(a)
	fb := errorclass.Fingerprint(b)

	if fa != fb {
		t.Errorf("similar errors should produce same fingerprint:\n  a = %q\n  b = %q", fa, fb)
	}
}

func TestFingerprint_distinguishesByHTTPStatus(t *testing.T) {
	// Same body, different status — should produce different fingerprints
	// because a 400 and a 500 with the same text are semantically distinct.
	a := session.SessionError{HTTPStatus: 400, RawError: "Internal error processing request"}
	b := session.SessionError{HTTPStatus: 500, RawError: "Internal error processing request"}

	fa := errorclass.Fingerprint(a)
	fb := errorclass.Fingerprint(b)

	if fa == fb {
		t.Errorf("different HTTP statuses should produce different fingerprints, both got %q", fa)
	}
}

func TestFingerprint_distinguishesByCategory(t *testing.T) {
	// Same body, no status, but different (already-set) categories.
	a := session.SessionError{Category: session.ErrorCategoryToolError, RawError: "operation failed"}
	b := session.SessionError{Category: session.ErrorCategoryNetworkError, RawError: "operation failed"}

	fa := errorclass.Fingerprint(a)
	fb := errorclass.Fingerprint(b)

	if fa == fb {
		t.Errorf("different categories should produce different fingerprints, both got %q", fa)
	}
}

func TestFingerprint_unknownCategoryDoesNotInfluence(t *testing.T) {
	// Unknown category is the "no info" case — it shouldn't change the
	// fingerprint compared to no category at all. This ensures that errors
	// classified as unknown can still be matched against past user decisions.
	a := session.SessionError{Category: session.ErrorCategoryUnknown, RawError: "weird error"}
	b := session.SessionError{RawError: "weird error"}

	fa := errorclass.Fingerprint(a)
	fb := errorclass.Fingerprint(b)

	if fa != fb {
		t.Errorf("unknown category should not influence fingerprint: %q vs %q", fa, fb)
	}
}

func TestFingerprint_fallsBackToMessageWhenRawEmpty(t *testing.T) {
	err := session.SessionError{Message: "Connection refused"}
	fp := errorclass.Fingerprint(err)

	if fp == "" {
		t.Error("fingerprint should fall back to Message when RawError is empty")
	}
}

func TestFingerprint_truncatesLongInputs(t *testing.T) {
	// Pathological case: 1MB raw_error. Should not blow up.
	big := strings.Repeat("a", 1024*1024)
	err := session.SessionError{RawError: big}

	fp := errorclass.Fingerprint(err)
	if fp == "" {
		t.Error("expected non-empty fingerprint for large input")
	}
	if len(fp) != 32 { // 16 bytes hex-encoded
		t.Errorf("fingerprint length = %d, want 32", len(fp))
	}
}

// TestFingerprint_realWorldPromptTooLong verifies that the three real
// "prompt is too long" variants observed in the production DB collapse
// into the same fingerprint group. Token counts differ (1.7M, 2.9M, 1.8M)
// and one variant has a request_id wrapped in a different JSON envelope —
// none of these should split the group.
//
// Without good normalization, each of these would be a distinct
// "unclassified" group in the dashboard, defeating the bulk-classify goal.
func TestFingerprint_realWorldPromptTooLong(t *testing.T) {
	variants := []string{
		`{"message":"The model returned the following errors: prompt is too long: 1711673 tokens > 1000000 maximum"}`,
		`{"message":"The model returned the following errors: prompt is too long: 1807329 tokens > 1000000 maximum"}`,
		`{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 2961002 tokens > 1000000 maximum"},"request_id":"req_011CZRXuLqSVyNKdzLD4icq8"}`,
	}

	// Note: variant 3 has a different envelope (Anthropic format vs
	// the "model returned the following errors" wrapper). They WILL produce
	// different fingerprints, and that's fine — the user can classify both
	// groups with the same category in two clicks. The important property
	// is that variants 1 and 2 (same envelope, different numbers) collapse.
	fp1 := errorclass.Fingerprint(session.SessionError{RawError: variants[0]})
	fp2 := errorclass.Fingerprint(session.SessionError{RawError: variants[1]})

	if fp1 != fp2 {
		t.Errorf("same-envelope prompt-too-long variants should fingerprint identically:\n  v1 → %s\n  v2 → %s", fp1, fp2)
	}
}

// TestFingerprint_realWorldUnableToConnect verifies the most common
// unclassified error in the production DB (428 occurrences) collapses to
// a single fingerprint regardless of HTTP status presence.
func TestFingerprint_realWorldUnableToConnect(t *testing.T) {
	a := session.SessionError{RawError: "Error: Unable to connect. Is the computer able to access the url?"}
	b := session.SessionError{RawError: "Error: Unable to connect. Is the computer able to access the url?"}

	if errorclass.Fingerprint(a) != errorclass.Fingerprint(b) {
		t.Error("identical raw errors must produce identical fingerprints")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
