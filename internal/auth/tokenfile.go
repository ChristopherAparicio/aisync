package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const tokenFileName = "token"

// SaveToken writes a JWT token to ~/.aisync/token.
func SaveToken(configDir, token string) error {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	path := filepath.Join(configDir, tokenFileName)
	return os.WriteFile(path, []byte(token), 0o600)
}

// LoadToken reads the JWT token from ~/.aisync/token.
// Returns empty string if the file doesn't exist.
func LoadToken(configDir string) string {
	path := filepath.Join(configDir, tokenFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// ClearToken removes the stored JWT token.
func ClearToken(configDir string) error {
	path := filepath.Join(configDir, tokenFileName)
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
