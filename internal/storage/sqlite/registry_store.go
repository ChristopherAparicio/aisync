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
