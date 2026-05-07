package sqlite

import (
	"fmt"
	"strings"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// NormalizeTag lowercases and trims a tag. Empty input returns "".
// Spaces are collapsed to a single hyphen so multi-word user input
// (e.g. "in progress") becomes a single token ("in-progress").
func NormalizeTag(tag string) string {
	t := strings.ToLower(strings.TrimSpace(tag))
	if t == "" {
		return ""
	}
	// Collapse internal whitespace runs into single hyphens.
	fields := strings.Fields(t)
	return strings.Join(fields, "-")
}

// AddTags attaches the given tags to a session. Duplicates are silently
// ignored (INSERT OR IGNORE). Empty tags are filtered out. Returns the
// number of new (actually inserted) rows.
func (s *Store) AddTags(sessionID session.ID, tags []string) (int, error) {
	if sessionID == "" {
		return 0, fmt.Errorf("AddTags: empty session id")
	}
	clean := dedupNormalizedTags(tags)
	if len(clean) == 0 {
		return 0, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC().Format(time.RFC3339)
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO session_tags (session_id, tag, created_at) VALUES (?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("prepare insert tag: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	inserted := 0
	for _, t := range clean {
		res, execErr := stmt.Exec(string(sessionID), t, now)
		if execErr != nil {
			return inserted, fmt.Errorf("insert tag %q: %w", t, execErr)
		}
		n, _ := res.RowsAffected()
		inserted += int(n)
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return inserted, fmt.Errorf("commit: %w", commitErr)
	}
	return inserted, nil
}

// RemoveTags detaches the given tags from a session. Tags absent from
// the session are silently ignored. Returns the number of rows actually
// removed.
func (s *Store) RemoveTags(sessionID session.ID, tags []string) (int, error) {
	if sessionID == "" {
		return 0, fmt.Errorf("RemoveTags: empty session id")
	}
	clean := dedupNormalizedTags(tags)
	if len(clean) == 0 {
		return 0, nil
	}

	placeholders := strings.Repeat("?,", len(clean))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, 0, len(clean)+1)
	args = append(args, string(sessionID))
	for _, t := range clean {
		args = append(args, t)
	}
	q := fmt.Sprintf(`DELETE FROM session_tags WHERE session_id = ? AND tag IN (%s)`, placeholders)
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return 0, fmt.Errorf("delete tags: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// GetTags returns all tags attached to a session, sorted alphabetically.
func (s *Store) GetTags(sessionID session.ID) ([]string, error) {
	rows, err := s.db.Query(`SELECT tag FROM session_tags WHERE session_id = ? ORDER BY tag ASC`, string(sessionID))
	if err != nil {
		return nil, fmt.Errorf("query tags: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var tags []string
	for rows.Next() {
		var t string
		if scanErr := rows.Scan(&t); scanErr != nil {
			return nil, fmt.Errorf("scan tag: %w", scanErr)
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

// GetTagsBatch returns tags for many sessions in one query. Sessions
// without tags are absent from the result map.
func (s *Store) GetTagsBatch(ids []session.ID) (map[session.ID][]string, error) {
	if len(ids) == 0 {
		return map[session.ID][]string{}, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		args = append(args, string(id))
	}
	q := fmt.Sprintf(`SELECT session_id, tag FROM session_tags WHERE session_id IN (%s) ORDER BY session_id, tag`, placeholders)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("query tags batch: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[session.ID][]string)
	for rows.Next() {
		var sid, tag string
		if scanErr := rows.Scan(&sid, &tag); scanErr != nil {
			return nil, fmt.Errorf("scan tag batch row: %w", scanErr)
		}
		out[session.ID(sid)] = append(out[session.ID(sid)], tag)
	}
	return out, rows.Err()
}

// ListAllTags returns all distinct tags across all sessions, with their
// counts, sorted by descending count then alphabetical tag.
func (s *Store) ListAllTags() ([]session.TagCount, error) {
	rows, err := s.db.Query(`SELECT tag, COUNT(*) AS n FROM session_tags GROUP BY tag ORDER BY n DESC, tag ASC`)
	if err != nil {
		return nil, fmt.Errorf("query all tags: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []session.TagCount
	for rows.Next() {
		var tc session.TagCount
		if scanErr := rows.Scan(&tc.Tag, &tc.Count); scanErr != nil {
			return nil, fmt.Errorf("scan tag count: %w", scanErr)
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}

// FilterSessionIDsByTags returns the subset of session IDs that have
// ALL the given tags (AND semantics). An empty tag list returns all
// session IDs from the input set unchanged.
func (s *Store) FilterSessionIDsByTags(ids []session.ID, tags []string) ([]session.ID, error) {
	clean := dedupNormalizedTags(tags)
	if len(clean) == 0 || len(ids) == 0 {
		return ids, nil
	}

	idPlaceholders := strings.Repeat("?,", len(ids))
	idPlaceholders = idPlaceholders[:len(idPlaceholders)-1]
	tagPlaceholders := strings.Repeat("?,", len(clean))
	tagPlaceholders = tagPlaceholders[:len(tagPlaceholders)-1]

	args := make([]interface{}, 0, len(ids)+len(clean)+1)
	for _, id := range ids {
		args = append(args, string(id))
	}
	for _, t := range clean {
		args = append(args, t)
	}
	args = append(args, len(clean))

	q := fmt.Sprintf(`
		SELECT session_id
		FROM session_tags
		WHERE session_id IN (%s) AND tag IN (%s)
		GROUP BY session_id
		HAVING COUNT(DISTINCT tag) = ?
	`, idPlaceholders, tagPlaceholders)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("filter by tags: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []session.ID
	for rows.Next() {
		var sid string
		if scanErr := rows.Scan(&sid); scanErr != nil {
			return nil, fmt.Errorf("scan filtered id: %w", scanErr)
		}
		out = append(out, session.ID(sid))
	}
	return out, rows.Err()
}

// dedupNormalizedTags lowercases, trims, and deduplicates while preserving
// first-seen order. Empty inputs are dropped.
func dedupNormalizedTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, raw := range tags {
		t := NormalizeTag(raw)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}
