package session

import (
	"testing"
)

func TestParseProviderName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    ProviderName
		wantErr bool
	}{
		{name: "valid claude-code", input: "claude-code", want: ProviderClaudeCode, wantErr: false},
		{name: "valid opencode", input: "opencode", want: ProviderOpenCode, wantErr: false},
		{name: "valid cursor", input: "cursor", want: ProviderCursor, wantErr: false},
		{name: "case insensitive", input: "Claude-Code", want: ProviderClaudeCode, wantErr: false},
		{name: "trimmed", input: "  opencode  ", want: ProviderOpenCode, wantErr: false},
		{name: "invalid provider", input: "copilot", want: "", wantErr: true},
		{name: "empty string", input: "", want: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseProviderName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseProviderName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseProviderName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestProviderName_Valid(t *testing.T) {
	tests := []struct {
		name  string
		input ProviderName
		want  bool
	}{
		{name: "claude-code is valid", input: ProviderClaudeCode, want: true},
		{name: "opencode is valid", input: ProviderOpenCode, want: true},
		{name: "cursor is valid", input: ProviderCursor, want: true},
		{name: "unknown is invalid", input: ProviderName("unknown"), want: false},
		{name: "empty is invalid", input: ProviderName(""), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.input.Valid(); got != tt.want {
				t.Errorf("ProviderName(%q).Valid() = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseStorageMode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    StorageMode
		wantErr bool
	}{
		{name: "full", input: "full", want: StorageModeFull, wantErr: false},
		{name: "compact", input: "compact", want: StorageModeCompact, wantErr: false},
		{name: "summary", input: "summary", want: StorageModeSummary, wantErr: false},
		{name: "case insensitive", input: "FULL", want: StorageModeFull, wantErr: false},
		{name: "invalid", input: "minimal", want: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseStorageMode(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseStorageMode(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseStorageMode(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseSecretMode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    SecretMode
		wantErr bool
	}{
		{name: "mask", input: "mask", want: SecretModeMask, wantErr: false},
		{name: "warn", input: "warn", want: SecretModeWarn, wantErr: false},
		{name: "block", input: "block", want: SecretModeBlock, wantErr: false},
		{name: "invalid", input: "ignore", want: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSecretMode(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseSecretMode(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseSecretMode(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseMessageRole(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    MessageRole
		wantErr bool
	}{
		{name: "user", input: "user", want: RoleUser, wantErr: false},
		{name: "assistant", input: "assistant", want: RoleAssistant, wantErr: false},
		{name: "system", input: "system", want: RoleSystem, wantErr: false},
		{name: "invalid", input: "tool", want: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMessageRole(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseMessageRole(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseMessageRole(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseChangeType(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    ChangeType
		wantErr bool
	}{
		{name: "created", input: "created", want: ChangeCreated, wantErr: false},
		{name: "modified", input: "modified", want: ChangeModified, wantErr: false},
		{name: "deleted", input: "deleted", want: ChangeDeleted, wantErr: false},
		{name: "read", input: "read", want: ChangeRead, wantErr: false},
		{name: "invalid", input: "renamed", want: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseChangeType(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseChangeType(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseChangeType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseLinkType(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    LinkType
		wantErr bool
	}{
		{name: "branch", input: "branch", want: LinkBranch, wantErr: false},
		{name: "commit", input: "commit", want: LinkCommit, wantErr: false},
		{name: "pr", input: "pr", want: LinkPR, wantErr: false},
		{name: "invalid", input: "tag", want: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLinkType(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseLinkType(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseLinkType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseToolState(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    ToolState
		wantErr bool
	}{
		{name: "pending", input: "pending", want: ToolStatePending, wantErr: false},
		{name: "running", input: "running", want: ToolStateRunning, wantErr: false},
		{name: "completed", input: "completed", want: ToolStateCompleted, wantErr: false},
		{name: "error", input: "error", want: ToolStateError, wantErr: false},
		{name: "invalid", input: "cancelled", want: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseToolState(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseToolState(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseToolState(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    ID
		wantErr bool
	}{
		{name: "valid uuid", input: "a1b2c3d4-e5f6-7890-abcd-ef1234567890", want: ID("a1b2c3d4-e5f6-7890-abcd-ef1234567890"), wantErr: false},
		{name: "any non-empty string", input: "my-session", want: ID("my-session"), wantErr: false},
		{name: "empty is invalid", input: "", want: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseID(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseID(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewID(t *testing.T) {
	id1 := NewID()
	id2 := NewID()

	if id1 == "" {
		t.Error("NewID() returned empty string")
	}
	if id1 == id2 {
		t.Error("NewID() returned same ID twice")
	}
}

func TestProviderName_String(t *testing.T) {
	if got := ProviderClaudeCode.String(); got != "claude-code" {
		t.Errorf("ProviderClaudeCode.String() = %q, want %q", got, "claude-code")
	}
}

func TestStorageMode_String(t *testing.T) {
	if got := StorageModeFull.String(); got != "full" {
		t.Errorf("StorageModeFull.String() = %q, want %q", got, "full")
	}
}
