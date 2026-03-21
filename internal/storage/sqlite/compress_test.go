package sqlite

import (
	"bytes"
	"strings"
	"testing"
)

func TestCompressDecompress_RoundTrip(t *testing.T) {
	original := []byte(`{"id":"sess-1","provider":"openai","messages":[{"role":"user","content":"hello"}]}`)

	compressed, err := compressPayload(original)
	if err != nil {
		t.Fatalf("compressPayload: %v", err)
	}

	decompressed, err := decompressPayload(compressed)
	if err != nil {
		t.Fatalf("decompressPayload: %v", err)
	}

	if !bytes.Equal(original, decompressed) {
		t.Fatalf("round-trip mismatch:\n  want: %s\n  got:  %s", original, decompressed)
	}
}

func TestDecompressPayload_RawJSON(t *testing.T) {
	// Simulate a legacy uncompressed payload (raw JSON starting with '{').
	raw := []byte(`{"id":"legacy-session","provider":"anthropic"}`)

	out, err := decompressPayload(raw)
	if err != nil {
		t.Fatalf("decompressPayload(raw): %v", err)
	}

	if !bytes.Equal(raw, out) {
		t.Fatalf("expected raw JSON to pass through unchanged:\n  want: %s\n  got:  %s", raw, out)
	}
}

func TestDecompressPayload_Compressed(t *testing.T) {
	original := []byte(`{"messages":[{"role":"assistant","content":"world"}]}`)

	compressed, err := compressPayload(original)
	if err != nil {
		t.Fatalf("compressPayload: %v", err)
	}

	// Verify the compressed output starts with zstd magic bytes.
	if !isCompressed(compressed) {
		t.Fatalf("compressed data does not start with zstd magic bytes; got first 4 bytes: %x", compressed[:4])
	}

	decompressed, err := decompressPayload(compressed)
	if err != nil {
		t.Fatalf("decompressPayload: %v", err)
	}

	if !bytes.Equal(original, decompressed) {
		t.Fatalf("decompressed mismatch:\n  want: %s\n  got:  %s", original, decompressed)
	}
}

func TestCompressPayload_ReducesSize(t *testing.T) {
	// Build a large, repetitive JSON payload that compresses well.
	var sb strings.Builder
	sb.WriteString(`{"messages":[`)
	for i := range 500 {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"role":"user","content":"this is a repeated test message for compression benchmarking purposes"}`)
	}
	sb.WriteString(`]}`)
	large := []byte(sb.String())

	compressed, err := compressPayload(large)
	if err != nil {
		t.Fatalf("compressPayload: %v", err)
	}

	t.Logf("original=%d bytes, compressed=%d bytes, ratio=%.1f%%",
		len(large), len(compressed), float64(len(compressed))*100/float64(len(large)))

	if len(compressed) >= len(large) {
		t.Fatalf("compressed size (%d) should be smaller than original (%d)", len(compressed), len(large))
	}
}

func TestDecompressPayload_RawJSONArray(t *testing.T) {
	// JSON arrays start with '[', which should also be recognised as raw JSON.
	raw := []byte(`[{"id":"a"},{"id":"b"}]`)

	out, err := decompressPayload(raw)
	if err != nil {
		t.Fatalf("decompressPayload(array): %v", err)
	}

	if !bytes.Equal(raw, out) {
		t.Fatalf("expected raw JSON array to pass through unchanged:\n  want: %s\n  got:  %s", raw, out)
	}
}

func TestDecompressPayload_Empty(t *testing.T) {
	out, err := decompressPayload(nil)
	if err != nil {
		t.Fatalf("decompressPayload(nil): %v", err)
	}
	if out != nil {
		t.Fatalf("expected nil, got %v", out)
	}

	out, err = decompressPayload([]byte{})
	if err != nil {
		t.Fatalf("decompressPayload(empty): %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty, got %v", out)
	}
}
