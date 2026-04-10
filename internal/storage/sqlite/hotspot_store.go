package sqlite

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// GetHotspots retrieves pre-computed hot-spots for a session.
// Returns (nil, nil) when no row exists.
func (s *Store) GetHotspots(id session.ID) (*session.SessionHotspots, error) {
	var payload []byte
	err := s.db.QueryRow(
		`SELECT payload FROM session_hotspots WHERE session_id = ?`, string(id),
	).Scan(&payload)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get session_hotspots for %s: %w", id, err)
	}

	h, err := decompressHotspots(payload)
	if err != nil {
		return nil, fmt.Errorf("decompress session_hotspots for %s: %w", id, err)
	}
	return h, nil
}

// SetHotspots persists hot-spots for a session (upsert).
// The payload is gzip-compressed before storage.
func (s *Store) SetHotspots(id session.ID, h session.SessionHotspots, schemaVersion int) error {
	payload, err := compressHotspots(h)
	if err != nil {
		return fmt.Errorf("compress session_hotspots for %s: %w", id, err)
	}

	_, err = s.db.Exec(`
		INSERT INTO session_hotspots (session_id, payload, schema_version, computed_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			payload        = excluded.payload,
			schema_version = excluded.schema_version,
			computed_at    = excluded.computed_at`,
		string(id), payload, schemaVersion, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("upsert session_hotspots for %s: %w", id, err)
	}
	return nil
}

// ListSessionsNeedingHotspots returns session IDs that either have no row
// in session_hotspots or have schema_version < minSchemaVersion.
func (s *Store) ListSessionsNeedingHotspots(minSchemaVersion int, limit int) ([]session.ID, error) {
	rows, err := s.db.Query(`
		SELECT s.id
		FROM sessions s
		LEFT JOIN session_hotspots sh ON s.id = sh.session_id
		WHERE sh.session_id IS NULL
		   OR sh.schema_version < ?
		ORDER BY s.created_at DESC
		LIMIT ?`,
		minSchemaVersion, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions needing hotspots: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []session.ID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan session id for hotspots: %w", err)
		}
		ids = append(ids, session.ID(id))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions needing hotspots: %w", err)
	}
	return ids, nil
}

// compressHotspots serializes SessionHotspots to gzip-compressed JSON.
func compressHotspots(h session.SessionHotspots) ([]byte, error) {
	raw, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(raw); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decompressHotspots inflates gzip-compressed JSON into SessionHotspots.
func decompressHotspots(data []byte) (*session.SessionHotspots, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()

	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var h session.SessionHotspots
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, err
	}
	return &h, nil
}
