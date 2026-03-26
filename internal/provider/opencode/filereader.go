package opencode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

// fileReader reads OpenCode sessions from the legacy file-based storage
// at ~/.local/share/opencode/storage/.
type fileReader struct {
	storagePath string
}

func newFileReader(dataHome string) *fileReader {
	return &fileReader{
		storagePath: filepath.Join(dataHome, storageDir),
	}
}

func (r *fileReader) findProjectID(worktreePath string) (string, error) {
	projectsPath := filepath.Join(r.storagePath, projectDir)
	entries, err := os.ReadDir(projectsPath)
	if err != nil {
		return "", fmt.Errorf("reading projects directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if entry.Name() == "global.json" {
			continue
		}

		data, readErr := os.ReadFile(filepath.Join(projectsPath, entry.Name()))
		if readErr != nil {
			continue
		}

		var proj ocProject
		if unmarshalErr := json.Unmarshal(data, &proj); unmarshalErr != nil {
			continue
		}

		if proj.Worktree == worktreePath {
			return proj.ID, nil
		}
	}

	return "", session.ErrSessionNotFound
}

func (r *fileReader) listSessions(projectID string) ([]ocSession, error) {
	sessionsPath := filepath.Join(r.storagePath, sessionDir, projectID)
	entries, err := os.ReadDir(sessionsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading sessions directory: %w", err)
	}

	var sessions []ocSession
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, readErr := os.ReadFile(filepath.Join(sessionsPath, entry.Name()))
		if readErr != nil {
			continue
		}

		var sess ocSession
		if unmarshalErr := json.Unmarshal(data, &sess); unmarshalErr != nil {
			continue
		}

		sessions = append(sessions, sess)
	}

	return sessions, nil
}

func (r *fileReader) readSession(sessionID string) (*ocSession, error) {
	sessionsRoot := filepath.Join(r.storagePath, sessionDir)
	projectDirs, err := os.ReadDir(sessionsRoot)
	if err != nil {
		return nil, fmt.Errorf("reading session root: %w", err)
	}

	fileName := sessionID + ".json"
	for _, dir := range projectDirs {
		if !dir.IsDir() {
			continue
		}
		path := filepath.Join(sessionsRoot, dir.Name(), fileName)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}

		var sess ocSession
		if unmarshalErr := json.Unmarshal(data, &sess); unmarshalErr != nil {
			continue
		}
		return &sess, nil
	}

	return nil, session.ErrSessionNotFound
}

func (r *fileReader) loadMessages(sessionID string) ([]ocMessage, error) {
	messagesPath := filepath.Join(r.storagePath, messageDir, sessionID)
	entries, err := os.ReadDir(messagesPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading messages directory: %w", err)
	}

	var messages []ocMessage
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, readErr := os.ReadFile(filepath.Join(messagesPath, entry.Name()))
		if readErr != nil {
			continue
		}

		var msg ocMessage
		if unmarshalErr := json.Unmarshal(data, &msg); unmarshalErr != nil {
			continue
		}
		messages = append(messages, msg)
	}

	return messages, nil
}

func (r *fileReader) loadAllPartsForSession(sessionID string) (map[string][]ocPart, error) {
	// File reader doesn't support batch loading — fall back to per-message loading.
	return nil, nil
}

func (r *fileReader) loadParts(messageID string) ([]ocPart, error) {
	partsPath := filepath.Join(r.storagePath, partDir, messageID)
	entries, err := os.ReadDir(partsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading parts directory: %w", err)
	}

	var parts []ocPart
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, readErr := os.ReadFile(filepath.Join(partsPath, entry.Name()))
		if readErr != nil {
			continue
		}

		var part ocPart
		if unmarshalErr := json.Unmarshal(data, &part); unmarshalErr != nil {
			continue
		}
		parts = append(parts, part)
	}

	return parts, nil
}

func (r *fileReader) countMessages(sessionID string) int {
	messagesPath := filepath.Join(r.storagePath, messageDir, sessionID)
	entries, err := os.ReadDir(messagesPath)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			count++
		}
	}
	return count
}

func (r *fileReader) findChildSessions(parentID string) ([]ocSession, error) {
	sessionsRoot := filepath.Join(r.storagePath, sessionDir)
	projectDirs, err := os.ReadDir(sessionsRoot)
	if err != nil {
		return nil, err
	}

	var children []ocSession
	for _, dir := range projectDirs {
		if !dir.IsDir() {
			continue
		}
		dirPath := filepath.Join(sessionsRoot, dir.Name())
		entries, readErr := os.ReadDir(dirPath)
		if readErr != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}

			data, fileErr := os.ReadFile(filepath.Join(dirPath, entry.Name()))
			if fileErr != nil {
				continue
			}

			var sess ocSession
			if unmarshalErr := json.Unmarshal(data, &sess); unmarshalErr != nil {
				continue
			}

			if sess.ParentID == parentID {
				children = append(children, sess)
			}
		}
	}

	return children, nil
}

func (r *fileReader) sessionUpdatedAt(sessionID string) int64 {
	sess, err := r.readSession(sessionID)
	if err != nil {
		return 0
	}
	return sess.Time.Updated
}

func (r *fileReader) listAllProjects() ([]ocProjectInfo, error) {
	projectsPath := filepath.Join(r.storagePath, projectDir)
	entries, err := os.ReadDir(projectsPath)
	if err != nil {
		return nil, fmt.Errorf("reading projects directory: %w", err)
	}

	var projects []ocProjectInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || entry.Name() == "global.json" {
			continue
		}

		data, readErr := os.ReadFile(filepath.Join(projectsPath, entry.Name()))
		if readErr != nil {
			continue
		}

		var proj ocProject
		if unmarshalErr := json.Unmarshal(data, &proj); unmarshalErr != nil {
			continue
		}

		// Count sessions for this project.
		sessDir := filepath.Join(r.storagePath, sessionDir, proj.ID)
		sessCount := 0
		if sessEntries, dirErr := os.ReadDir(sessDir); dirErr == nil {
			for _, se := range sessEntries {
				if strings.HasSuffix(se.Name(), ".json") {
					sessCount++
				}
			}
		}

		projects = append(projects, ocProjectInfo{
			ID:           proj.ID,
			Worktree:     proj.Worktree,
			SessionCount: sessCount,
		})
	}

	return projects, nil
}
