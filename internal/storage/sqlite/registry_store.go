package sqlite

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/registry"
	"github.com/google/uuid"
)

// SaveProjectSnapshot persists a capability snapshot.
func (s *Store) SaveProjectSnapshot(snapshot *registry.ProjectSnapshot) error {
	if snapshot.ID == "" {
		snapshot.ID = uuid.New().String()
	}
	if snapshot.ScannedAt == "" {
		snapshot.ScannedAt = time.Now().UTC().Format(time.RFC3339)
	}

	payload, err := json.Marshal(snapshot.Project)
	if err != nil {
		return fmt.Errorf("marshal project snapshot: %w", err)
	}

	_, err = s.db.Exec(`INSERT OR REPLACE INTO project_snapshots
		(id, project_path, scanned_at, change_type,
		 capabilities_added, capabilities_removed,
		 mcp_servers_added, mcp_servers_removed, payload)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snapshot.ID,
		snapshot.ProjectPath,
		snapshot.ScannedAt,
		snapshot.ChangeType,
		snapshot.CapabilitiesAdded,
		snapshot.CapabilitiesRemoved,
		snapshot.MCPServersAdded,
		snapshot.MCPServersRemoved,
		payload,
	)
	return err
}

// GetLatestSnapshot returns the most recent snapshot for a project.
func (s *Store) GetLatestSnapshot(projectPath string) (*registry.ProjectSnapshot, error) {
	row := s.db.QueryRow(`SELECT id, project_path, scanned_at, change_type,
		capabilities_added, capabilities_removed,
		mcp_servers_added, mcp_servers_removed, payload
		FROM project_snapshots
		WHERE project_path = ?
		ORDER BY scanned_at DESC
		LIMIT 1`, projectPath)

	return scanSnapshot(row)
}

// ListSnapshots returns snapshots for a project, newest first.
func (s *Store) ListSnapshots(projectPath string, limit int) ([]registry.ProjectSnapshot, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.Query(`SELECT id, project_path, scanned_at, change_type,
		capabilities_added, capabilities_removed,
		mcp_servers_added, mcp_servers_removed, payload
		FROM project_snapshots
		WHERE project_path = ?
		ORDER BY scanned_at DESC
		LIMIT ?`, projectPath, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var snapshots []registry.ProjectSnapshot
	for rows.Next() {
		var snap registry.ProjectSnapshot
		var payload []byte
		if err := rows.Scan(
			&snap.ID, &snap.ProjectPath, &snap.ScannedAt, &snap.ChangeType,
			&snap.CapabilitiesAdded, &snap.CapabilitiesRemoved,
			&snap.MCPServersAdded, &snap.MCPServersRemoved,
			&payload,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &snap.Project); err != nil {
			return nil, fmt.Errorf("unmarshal snapshot %s: %w", snap.ID, err)
		}
		snapshots = append(snapshots, snap)
	}
	return snapshots, rows.Err()
}

// UpsertCapabilities inserts or updates flat capability records for a project.
// Capabilities present in caps are marked active with updated last_seen.
// Capabilities NOT in caps but previously active for this project are deactivated.
func (s *Store) UpsertCapabilities(projectPath string, caps []registry.PersistedCapability) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC().Format(time.RFC3339)

	// Step 1: Mark all existing capabilities for this project as inactive.
	if _, err := tx.Exec(
		`UPDATE project_capabilities SET is_active = 0 WHERE project_path = ?`,
		projectPath,
	); err != nil {
		return fmt.Errorf("deactivate capabilities: %w", err)
	}

	// Step 2: Upsert each current capability — INSERT if new, UPDATE if exists.
	stmt, err := tx.Prepare(`INSERT INTO project_capabilities
		(id, project_path, name, kind, scope, is_active, first_seen, last_seen)
		VALUES (?, ?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT(project_path, name, kind) DO UPDATE SET
			scope = excluded.scope,
			is_active = 1,
			last_seen = excluded.last_seen`)
	if err != nil {
		return fmt.Errorf("prepare upsert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, cap := range caps {
		id := uuid.New().String()
		if _, err := stmt.Exec(id, projectPath, cap.Name, string(cap.Kind), string(cap.Scope), now, now); err != nil {
			return fmt.Errorf("upsert capability %s/%s: %w", cap.Kind, cap.Name, err)
		}
	}

	return tx.Commit()
}

// ListCapabilities returns persisted capabilities matching the filter.
func (s *Store) ListCapabilities(filter registry.CapabilityFilter) ([]registry.PersistedCapability, error) {
	query := `SELECT id, project_path, name, kind, scope, is_active, first_seen, last_seen
		FROM project_capabilities WHERE 1=1`
	var args []any

	if filter.ProjectPath != "" {
		query += " AND project_path = ?"
		args = append(args, filter.ProjectPath)
	}
	if filter.Kind != "" {
		query += " AND kind = ?"
		args = append(args, string(filter.Kind))
	}
	if filter.ActiveOnly {
		query += " AND is_active = 1"
	}

	query += " ORDER BY project_path, kind, name"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []registry.PersistedCapability
	for rows.Next() {
		var pc registry.PersistedCapability
		var kindStr, scopeStr, firstSeenStr, lastSeenStr string
		var isActive int

		if err := rows.Scan(&pc.ID, &pc.ProjectPath, &pc.Name,
			&kindStr, &scopeStr, &isActive, &firstSeenStr, &lastSeenStr); err != nil {
			return nil, err
		}

		pc.Kind = registry.CapabilityKind(kindStr)
		pc.Scope = registry.Scope(scopeStr)
		pc.IsActive = isActive == 1
		pc.FirstSeen, _ = time.Parse(time.RFC3339, firstSeenStr)
		pc.LastSeen, _ = time.Parse(time.RFC3339, lastSeenStr)

		result = append(result, pc)
	}
	return result, rows.Err()
}

// ListCapabilityProjects returns distinct project paths that have at least
// one persisted capability.
func (s *Store) ListCapabilityProjects() ([]string, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT project_path FROM project_capabilities ORDER BY project_path`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// scanner interface for both sql.Row and sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanSnapshot(row rowScanner) (*registry.ProjectSnapshot, error) {
	var snap registry.ProjectSnapshot
	var payload []byte
	err := row.Scan(
		&snap.ID, &snap.ProjectPath, &snap.ScannedAt, &snap.ChangeType,
		&snap.CapabilitiesAdded, &snap.CapabilitiesRemoved,
		&snap.MCPServersAdded, &snap.MCPServersRemoved,
		&payload,
	)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(payload, &snap.Project); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot %s: %w", snap.ID, err)
	}
	return &snap, nil
}
