// Package errorclass — fingerprint.go computes stable fingerprints for raw
// error strings so that "the same error occurring with different IDs/numbers/
// paths" can be grouped, counted, and bulk-classified by the user.
//
// Why a separate file from the classifiers?
//
//	Fingerprinting is a string-normalization concern, independent of any
//	classification rule. The same fingerprint can be reused by:
//	  - the unclassified-errors dashboard (group + count + present)
//	  - the FingerprintClassifier (auto-classify based on past user decisions)
//	  - the reclassification scheduler (bulk-update prior errors)
//
// Design notes:
//   - Fingerprints must be DETERMINISTIC: the same logical error always
//     produces the same fingerprint regardless of timestamps, request IDs,
//     numeric quantities, file paths, URLs, or message indices.
//   - Fingerprints must be SHORT: stored as a TEXT primary key in SQLite,
//     so we hash the normalized form to a fixed-size hex string.
//   - Fingerprints must be HUMAN-INSPECTABLE via the "sample_raw" we keep
//     alongside in the DB. The fingerprint itself is opaque; the sample is
//     for the UI.
package errorclass

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// Normalization regexes — compiled once, reused for every fingerprint call.
//
// The order of application matters: paths and URLs must be replaced before
// the bare-numbers regex, otherwise port numbers / line numbers inside a URL
// would get replaced first and leave a broken URL behind.
var (
	// UUIDs (8-4-4-4-12 hex with dashes).
	reUUID = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)

	// Provider request IDs: req_xxx, msg_xxx, toolu_xxx, call_xxx, etc.
	// These are common across Anthropic / OpenAI / etc. and are noise for grouping.
	reProviderID = regexp.MustCompile(`\b(?:req|msg|toolu|call|chatcmpl|run|asst|thread|file|batch|fc|ses)_[A-Za-z0-9]{6,}\b`)

	// Hex blobs ≥16 chars (commonly: long IDs, hashes, base64 chunks).
	// 16 is a safe lower bound that doesn't match short hex like "0x1234".
	reHex = regexp.MustCompile(`\b[0-9a-fA-F]{16,}\b`)

	// URLs (http/https with optional path/query).
	reURL = regexp.MustCompile(`https?://[^\s"'<>{}]+`)

	// Absolute Unix paths (starting with / and containing at least one /).
	// Avoids matching a single slash or root path.
	reUnixPath = regexp.MustCompile(`/(?:[A-Za-z0-9._-]+/)+[A-Za-z0-9._-]*`)

	// Windows-style paths (C:\foo\bar). Less common but appears in some tool errors.
	reWindowsPath = regexp.MustCompile(`[A-Za-z]:\\(?:[A-Za-z0-9._ -]+\\)*[A-Za-z0-9._ -]*`)

	// Bare numbers (≥1 digit, not part of an identifier). Replaced last so
	// that paths/URLs/IDs containing digits are already normalized first.
	// We allow optional leading minus and decimals.
	reNumber = regexp.MustCompile(`-?\b\d+(?:\.\d+)?\b`)

	// Run of whitespace → single space.
	reWhitespace = regexp.MustCompile(`\s+`)
)

// Normalize returns a normalized form of a raw error string suitable for
// fingerprinting. The normalization is intentionally aggressive: the goal is
// to collapse "the same error with different variable parts" into one canonical
// form. The output is NOT meant to be displayed to users — keep the original
// raw_error around for that.
//
// The normalization is stable: calling Normalize on already-normalized output
// produces the same string (idempotent).
func Normalize(raw string) string {
	if raw == "" {
		return ""
	}

	s := raw

	// Order matters — see comment at the regex declarations.
	s = reUUID.ReplaceAllString(s, "UUID")
	s = reProviderID.ReplaceAllString(s, "ID")
	s = reURL.ReplaceAllString(s, "URL")
	s = reUnixPath.ReplaceAllString(s, "PATH")
	s = reWindowsPath.ReplaceAllString(s, "PATH")
	s = reHex.ReplaceAllString(s, "HEX")
	s = reNumber.ReplaceAllString(s, "N")

	// Lowercase & collapse whitespace for case/format-insensitive matching.
	s = strings.ToLower(s)
	s = reWhitespace.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)

	return s
}

// fingerprintMaxLen caps the input to Fingerprint to avoid pathological
// allocations for very large raw_error blobs (e.g. huge stack traces or full
// HTML responses). 4096 chars is enough to capture any realistic error
// signature; the prefix is what determines grouping.
const fingerprintMaxLen = 4096

// Fingerprint computes a stable, opaque identifier for grouping similar errors.
//
// The fingerprint is a hex-encoded SHA-256 of the normalized form, optionally
// scoped by HTTP status and category (when known). Including these as prefixes
// ensures that two errors with the same message text but different status
// codes (e.g. a 400 vs a 500 with the same body) don't collide.
//
// Returns "" for empty input — callers should treat empty fingerprints as
// "ungroupable" rather than persisting them.
func Fingerprint(err session.SessionError) string {
	raw := err.RawError
	if raw == "" {
		raw = err.Message
	}
	if raw == "" {
		return ""
	}

	if len(raw) > fingerprintMaxLen {
		raw = raw[:fingerprintMaxLen]
	}

	normalized := Normalize(raw)
	if normalized == "" {
		return ""
	}

	// Scope the fingerprint by status + category so identical text under
	// different conditions don't collide.
	var b strings.Builder
	b.Grow(len(normalized) + 32)
	if err.HTTPStatus > 0 {
		b.WriteString("status=")
		// itoa without importing strconv — small int, manual works fine.
		writeInt(&b, err.HTTPStatus)
		b.WriteByte('|')
	}
	if err.Category != "" && err.Category != session.ErrorCategoryUnknown {
		b.WriteString("cat=")
		b.WriteString(string(err.Category))
		b.WriteByte('|')
	}
	b.WriteString(normalized)

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:16]) // 32 hex chars = 128 bits, plenty
}

// writeInt writes a non-negative int as decimal to a strings.Builder without
// allocating an intermediate string.
func writeInt(b *strings.Builder, n int) {
	if n == 0 {
		b.WriteByte('0')
		return
	}
	if n < 0 {
		b.WriteByte('-')
		n = -n
	}
	// Build digits in reverse, then write.
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	b.Write(buf[i:])
}
