package service

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ParseFileManifest extracts a list of file paths from a manifest's raw content, auto-detecting
// the format: content whose first non-whitespace byte is '[' is parsed as a JSON array (a
// malformed array is a hard error); otherwise it is parsed as one path per line, skipping blank
// lines and '#' comments and trimming surrounding whitespace.
func ParseFileManifest(content []byte) ([]string, error) {
	trimmed := strings.TrimSpace(string(content))

	if strings.HasPrefix(trimmed, "[") {
		var files []string
		if err := json.Unmarshal([]byte(trimmed), &files); err != nil {
			return nil, fmt.Errorf("parsing JSON manifest: %w", err)
		}
		return files, nil
	}

	var files []string
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		files = append(files, line)
	}
	return files, nil
}
