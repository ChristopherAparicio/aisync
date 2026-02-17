// Package platform provides platform detection and registry for code hosting services.
// It inspects git remote URLs to determine whether a repository is hosted on
// GitHub, GitLab, Bitbucket, or a self-hosted instance.
package platform

import (
	"strings"

	"github.com/ChristopherAparicio/aisync/internal/domain"
)

// knownHosts maps hostname patterns to platform names.
var knownHosts = map[string]domain.PlatformName{
	"github.com":    domain.PlatformGitHub,
	"gitlab.com":    domain.PlatformGitLab,
	"bitbucket.org": domain.PlatformBitbucket,
}

// DetectFromRemoteURL determines the platform from a git remote URL.
// Supports SSH (git@github.com:org/repo.git) and HTTPS (https://github.com/org/repo.git).
// Returns ErrPlatformNotDetected if the URL doesn't match any known platform.
func DetectFromRemoteURL(remoteURL string) (domain.PlatformName, error) {
	host := extractHost(remoteURL)
	if host == "" {
		return "", domain.ErrPlatformNotDetected
	}

	// Check exact match first
	if p, ok := knownHosts[host]; ok {
		return p, nil
	}

	// Check suffix match for self-hosted instances (e.g., github.example.com)
	for known, p := range knownHosts {
		// Skip the base domain itself (already checked above)
		if strings.HasSuffix(host, "."+known) {
			return p, nil
		}
	}

	return "", domain.ErrPlatformNotDetected
}

// extractHost extracts the hostname from a git remote URL.
// Handles:
//   - SSH:   git@github.com:org/repo.git
//   - HTTPS: https://github.com/org/repo.git
//   - SSH:   ssh://git@github.com/org/repo.git
func extractHost(remoteURL string) string {
	url := strings.TrimSpace(remoteURL)

	// SSH format: git@host:org/repo.git
	if strings.HasPrefix(url, "git@") {
		url = strings.TrimPrefix(url, "git@")
		if idx := strings.Index(url, ":"); idx > 0 {
			return strings.ToLower(url[:idx])
		}
		return ""
	}

	// SSH format: ssh://git@host/org/repo.git
	if strings.HasPrefix(url, "ssh://") {
		url = strings.TrimPrefix(url, "ssh://")
		if atIdx := strings.Index(url, "@"); atIdx >= 0 {
			url = url[atIdx+1:]
		}
		return extractHostFromPath(url)
	}

	// HTTPS format: https://host/org/repo.git
	for _, prefix := range []string{"https://", "http://"} {
		if strings.HasPrefix(url, prefix) {
			url = strings.TrimPrefix(url, prefix)
			return extractHostFromPath(url)
		}
	}

	return ""
}

// extractHostFromPath returns the host from a "host/path..." string.
func extractHostFromPath(s string) string {
	// Strip port if present (host:port/path)
	host := s
	if idx := strings.Index(host, "/"); idx > 0 {
		host = host[:idx]
	}
	// Remove port
	if idx := strings.Index(host, ":"); idx > 0 {
		host = host[:idx]
	}
	return strings.ToLower(host)
}
