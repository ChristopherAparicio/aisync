package platform

import (
	"errors"
	"testing"

	"github.com/ChristopherAparicio/aisync/internal/session"
)

func TestDetectFromRemoteURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		want    session.PlatformName
		wantErr bool
	}{
		// GitHub
		{
			name: "github SSH",
			url:  "git@github.com:org/repo.git",
			want: session.PlatformGitHub,
		},
		{
			name: "github HTTPS",
			url:  "https://github.com/org/repo.git",
			want: session.PlatformGitHub,
		},
		{
			name: "github HTTPS no .git",
			url:  "https://github.com/org/repo",
			want: session.PlatformGitHub,
		},
		{
			name: "github SSH protocol",
			url:  "ssh://git@github.com/org/repo.git",
			want: session.PlatformGitHub,
		},

		// GitLab
		{
			name: "gitlab SSH",
			url:  "git@gitlab.com:org/repo.git",
			want: session.PlatformGitLab,
		},
		{
			name: "gitlab HTTPS",
			url:  "https://gitlab.com/org/repo.git",
			want: session.PlatformGitLab,
		},

		// Bitbucket
		{
			name: "bitbucket SSH",
			url:  "git@bitbucket.org:org/repo.git",
			want: session.PlatformBitbucket,
		},
		{
			name: "bitbucket HTTPS",
			url:  "https://bitbucket.org/org/repo.git",
			want: session.PlatformBitbucket,
		},

		// Case insensitive
		{
			name: "github uppercase",
			url:  "git@GitHub.com:org/repo.git",
			want: session.PlatformGitHub,
		},

		// Unknown / unsupported
		{
			name:    "unknown host",
			url:     "git@custom-git.example.com:org/repo.git",
			wantErr: true,
		},
		{
			name:    "empty URL",
			url:     "",
			wantErr: true,
		},
		{
			name:    "garbage",
			url:     "not-a-url",
			wantErr: true,
		},

		// Whitespace handling
		{
			name: "whitespace trimmed",
			url:  "  git@github.com:org/repo.git  ",
			want: session.PlatformGitHub,
		},

		// HTTPS with port
		{
			name: "HTTPS with port",
			url:  "https://github.com:443/org/repo.git",
			want: session.PlatformGitHub,
		},

		// HTTP (unusual but valid)
		{
			name: "HTTP github",
			url:  "http://github.com/org/repo.git",
			want: session.PlatformGitHub,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DetectFromRemoteURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Errorf("DetectFromRemoteURL(%q) error = nil, want error", tt.url)
				}
				if !errors.Is(err, session.ErrPlatformNotDetected) {
					t.Errorf("error = %v, want ErrPlatformNotDetected", err)
				}
				return
			}
			if err != nil {
				t.Errorf("DetectFromRemoteURL(%q) error = %v", tt.url, err)
				return
			}
			if got != tt.want {
				t.Errorf("DetectFromRemoteURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestExtractHost(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"git@github.com:org/repo.git", "github.com"},
		{"https://github.com/org/repo.git", "github.com"},
		{"ssh://git@github.com/org/repo.git", "github.com"},
		{"https://GitHub.COM/org/repo.git", "github.com"},
		{"https://github.com:8443/org/repo.git", "github.com"},
		{"", ""},
		{"garbage", ""},
	}

	for _, tt := range tests {
		got := extractHost(tt.url)
		if got != tt.want {
			t.Errorf("extractHost(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}
