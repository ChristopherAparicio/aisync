package sqlite

import (
	"bytes"
	"fmt"
	"io"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// zstd frame magic number: 0x28 0xB5 0x2F 0xFD
var zstdMagic = []byte{0x28, 0xB5, 0x2F, 0xFD}

// encoderPool reuses zstd encoders to amortise allocation cost.
var encoderPool = sync.Pool{
	New: func() any {
		enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
		if err != nil {
			panic(fmt.Sprintf("creating zstd encoder: %v", err))
		}
		return enc
	},
}

// decoderPool reuses zstd decoders to amortise allocation cost.
var decoderPool = sync.Pool{
	New: func() any {
		dec, err := zstd.NewReader(nil)
		if err != nil {
			panic(fmt.Sprintf("creating zstd decoder: %v", err))
		}
		return dec
	},
}

// compressPayload compresses data using zstd.
// The output starts with the standard zstd frame magic bytes (0x28 0xB5 0x2F 0xFD),
// which allows decompressPayload to distinguish compressed from raw JSON blobs.
func compressPayload(data []byte) ([]byte, error) {
	enc := encoderPool.Get().(*zstd.Encoder)
	defer encoderPool.Put(enc)

	var buf bytes.Buffer
	enc.Reset(&buf)

	if _, err := enc.Write(data); err != nil {
		return nil, fmt.Errorf("zstd write: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("zstd close: %w", err)
	}

	return buf.Bytes(), nil
}

// decompressPayload returns raw JSON from a payload BLOB.
// It is backwards-compatible: if the blob starts with zstd magic bytes it is
// decompressed; otherwise the blob is returned as-is (legacy uncompressed JSON).
func decompressPayload(data []byte) ([]byte, error) {
	if !isCompressed(data) {
		return data, nil
	}

	dec := decoderPool.Get().(*zstd.Decoder)
	defer decoderPool.Put(dec)

	if err := dec.Reset(bytes.NewReader(data)); err != nil {
		return nil, fmt.Errorf("zstd reset: %w", err)
	}

	out, err := io.ReadAll(dec)
	if err != nil {
		return nil, fmt.Errorf("zstd decompress: %w", err)
	}

	return out, nil
}

// isCompressed reports whether data begins with the zstd frame magic number.
func isCompressed(data []byte) bool {
	return len(data) >= 4 && bytes.Equal(data[:4], zstdMagic)
}
