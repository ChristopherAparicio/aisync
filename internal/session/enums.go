package session

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ── ProviderName ──

// ProviderName identifies an AI coding tool.
type ProviderName string

// Known provider names.
const (
	ProviderClaudeCode ProviderName = "claude-code"
	ProviderOpenCode   ProviderName = "opencode"
	ProviderCursor     ProviderName = "cursor"
	ProviderParlay     ProviderName = "parlay"
	ProviderOllama     ProviderName = "ollama"
)

var allProviders = []ProviderName{
	ProviderClaudeCode,
	ProviderOpenCode,
	ProviderCursor,
	ProviderParlay,
	ProviderOllama,
}

// Valid reports whether p is a known provider name.
func (p ProviderName) Valid() bool {
	for _, v := range allProviders {
		if p == v {
			return true
		}
	}
	return false
}

// ParseProviderName converts a raw string to a validated ProviderName.
func ParseProviderName(s string) (ProviderName, error) {
	p := ProviderName(strings.ToLower(strings.TrimSpace(s)))
	if !p.Valid() {
		return "", fmt.Errorf("unknown provider %q: valid values are %v", s, allProviders)
	}
	return p, nil
}

// String returns the string representation.
func (p ProviderName) String() string {
	return string(p)
}

// ── SessionStatus ──

// SessionStatus represents the lifecycle state of a session.
type SessionStatus string

const (
	// StatusActive means the session is currently being worked on
	// (source updated within the last hour).
	StatusActive SessionStatus = "active"

	// StatusIdle means the session has no recent updates
	// (last update between 1h and 7d ago).
	StatusIdle SessionStatus = "idle"

	// StatusArchived means the session has not been updated for a long time
	// (last update > 7 days ago).
	StatusArchived SessionStatus = "archived"
)

// DetectSessionStatus determines the status based on the source update time.
// This is a heuristic — neither OpenCode nor Claude track session lifecycle state.
func DetectSessionStatus(sourceUpdatedAt int64, lastCaptured time.Time) SessionStatus {
	// Use the most recent timestamp available.
	var lastActivity time.Time
	if sourceUpdatedAt > 0 {
		lastActivity = time.UnixMilli(sourceUpdatedAt)
	} else if !lastCaptured.IsZero() {
		lastActivity = lastCaptured
	} else {
		return StatusArchived
	}

	age := time.Since(lastActivity)
	switch {
	case age < 1*time.Hour:
		return StatusActive
	case age < 7*24*time.Hour:
		return StatusIdle
	default:
		return StatusArchived
	}
}

// ── StorageMode ──

// StorageMode controls how much of a session is stored.
type StorageMode string

// Known storage modes.
const (
	StorageModeFull    StorageMode = "full"
	StorageModeCompact StorageMode = "compact"
	StorageModeSummary StorageMode = "summary"
)

var allStorageModes = []StorageMode{
	StorageModeFull,
	StorageModeCompact,
	StorageModeSummary,
}

// Valid reports whether m is a known storage mode.
func (m StorageMode) Valid() bool {
	for _, v := range allStorageModes {
		if m == v {
			return true
		}
	}
	return false
}

// ParseStorageMode converts a raw string to a validated StorageMode.
func ParseStorageMode(s string) (StorageMode, error) {
	m := StorageMode(strings.ToLower(strings.TrimSpace(s)))
	if !m.Valid() {
		return "", fmt.Errorf("unknown storage mode %q: valid values are %v", s, allStorageModes)
	}
	return m, nil
}

// String returns the string representation.
func (m StorageMode) String() string {
	return string(m)
}

// ── SecretMode ──

// SecretMode controls how detected secrets are handled.
type SecretMode string

// Known secret handling modes.
const (
	SecretModeMask  SecretMode = "mask"
	SecretModeWarn  SecretMode = "warn"
	SecretModeBlock SecretMode = "block"
)

var allSecretModes = []SecretMode{
	SecretModeMask,
	SecretModeWarn,
	SecretModeBlock,
}

// Valid reports whether m is a known secret mode.
func (m SecretMode) Valid() bool {
	for _, v := range allSecretModes {
		if m == v {
			return true
		}
	}
	return false
}

// ParseSecretMode converts a raw string to a validated SecretMode.
func ParseSecretMode(s string) (SecretMode, error) {
	m := SecretMode(strings.ToLower(strings.TrimSpace(s)))
	if !m.Valid() {
		return "", fmt.Errorf("unknown secret mode %q: valid values are %v", s, allSecretModes)
	}
	return m, nil
}

// String returns the string representation.
func (m SecretMode) String() string {
	return string(m)
}

// ── MessageRole ──

// MessageRole identifies who sent a message.
type MessageRole string

// Known message roles.
const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleSystem    MessageRole = "system"
)

var allMessageRoles = []MessageRole{
	RoleUser,
	RoleAssistant,
	RoleSystem,
}

// Valid reports whether r is a known message role.
func (r MessageRole) Valid() bool {
	for _, v := range allMessageRoles {
		if r == v {
			return true
		}
	}
	return false
}

// ParseMessageRole converts a raw string to a validated MessageRole.
func ParseMessageRole(s string) (MessageRole, error) {
	r := MessageRole(strings.ToLower(strings.TrimSpace(s)))
	if !r.Valid() {
		return "", fmt.Errorf("unknown message role %q: valid values are %v", s, allMessageRoles)
	}
	return r, nil
}

// String returns the string representation.
func (r MessageRole) String() string {
	return string(r)
}

// ── ChangeType ──

// ChangeType describes what happened to a file.
type ChangeType string

// Known file change types.
const (
	ChangeCreated  ChangeType = "created"
	ChangeModified ChangeType = "modified"
	ChangeDeleted  ChangeType = "deleted"
	ChangeRead     ChangeType = "read"
)

var allChangeTypes = []ChangeType{
	ChangeCreated,
	ChangeModified,
	ChangeDeleted,
	ChangeRead,
}

// Valid reports whether c is a known change type.
func (c ChangeType) Valid() bool {
	for _, v := range allChangeTypes {
		if c == v {
			return true
		}
	}
	return false
}

// ParseChangeType converts a raw string to a validated ChangeType.
func ParseChangeType(s string) (ChangeType, error) {
	c := ChangeType(strings.ToLower(strings.TrimSpace(s)))
	if !c.Valid() {
		return "", fmt.Errorf("unknown change type %q: valid values are %v", s, allChangeTypes)
	}
	return c, nil
}

// String returns the string representation.
func (c ChangeType) String() string {
	return string(c)
}

// ── LinkType ──

// LinkType describes how a session is linked to a git object.
type LinkType string

// Known link types.
const (
	LinkBranch LinkType = "branch"
	LinkCommit LinkType = "commit"
	LinkPR     LinkType = "pr"
	LinkTicket LinkType = "ticket" // external ticket reference (e.g. "OMO-250")
)

var allLinkTypes = []LinkType{
	LinkBranch,
	LinkCommit,
	LinkPR,
	LinkTicket,
}

// Valid reports whether l is a known link type.
func (l LinkType) Valid() bool {
	for _, v := range allLinkTypes {
		if l == v {
			return true
		}
	}
	return false
}

// ParseLinkType converts a raw string to a validated LinkType.
func ParseLinkType(s string) (LinkType, error) {
	l := LinkType(strings.ToLower(strings.TrimSpace(s)))
	if !l.Valid() {
		return "", fmt.Errorf("unknown link type %q: valid values are %v", s, allLinkTypes)
	}
	return l, nil
}

// String returns the string representation.
func (l LinkType) String() string {
	return string(l)
}

// ── SessionLinkType ──

// SessionLinkType describes how two sessions are related.
type SessionLinkType string

// Known session-to-session link types.
const (
	SessionLinkDelegatedTo   SessionLinkType = "delegated_to"   // this session delegated work to the target
	SessionLinkDelegatedFrom SessionLinkType = "delegated_from" // this session was delegated from the source
	SessionLinkRelated       SessionLinkType = "related"        // generic relationship
	SessionLinkContinuation  SessionLinkType = "continuation"   // target continues the work of source
	SessionLinkFollowUp      SessionLinkType = "follow_up"      // target is a follow-up to source
	SessionLinkReplayOf      SessionLinkType = "replay_of"      // this session is a replay of the source
)

var allSessionLinkTypes = []SessionLinkType{
	SessionLinkDelegatedTo,
	SessionLinkDelegatedFrom,
	SessionLinkRelated,
	SessionLinkContinuation,
	SessionLinkFollowUp,
	SessionLinkReplayOf,
}

// Valid reports whether l is a known session link type.
func (l SessionLinkType) Valid() bool {
	for _, v := range allSessionLinkTypes {
		if l == v {
			return true
		}
	}
	return false
}

// ParseSessionLinkType converts a raw string to a validated SessionLinkType.
func ParseSessionLinkType(s string) (SessionLinkType, error) {
	l := SessionLinkType(strings.ToLower(strings.TrimSpace(s)))
	if !l.Valid() {
		return "", fmt.Errorf("unknown session link type %q: valid values are %v", s, allSessionLinkTypes)
	}
	return l, nil
}

// String returns the string representation.
func (l SessionLinkType) String() string {
	return string(l)
}

// Inverse returns the complementary link type for bidirectional linking.
// For example, DelegatedTo → DelegatedFrom and vice versa.
// Returns the same type for symmetric relationships (Related).
func (l SessionLinkType) Inverse() SessionLinkType {
	switch l {
	case SessionLinkDelegatedTo:
		return SessionLinkDelegatedFrom
	case SessionLinkDelegatedFrom:
		return SessionLinkDelegatedTo
	case SessionLinkContinuation:
		return SessionLinkFollowUp
	case SessionLinkFollowUp:
		return SessionLinkContinuation
	default:
		return l // symmetric
	}
}

// ── ToolState ──

// ToolState represents the lifecycle state of a tool call.
type ToolState string

// Known tool lifecycle states.
const (
	ToolStatePending   ToolState = "pending"
	ToolStateRunning   ToolState = "running"
	ToolStateCompleted ToolState = "completed"
	ToolStateError     ToolState = "error"
)

var allToolStates = []ToolState{
	ToolStatePending,
	ToolStateRunning,
	ToolStateCompleted,
	ToolStateError,
}

// Valid reports whether s is a known tool state.
func (s ToolState) Valid() bool {
	for _, v := range allToolStates {
		if s == v {
			return true
		}
	}
	return false
}

// ParseToolState converts a raw string to a validated ToolState.
func ParseToolState(s string) (ToolState, error) {
	ts := ToolState(strings.ToLower(strings.TrimSpace(s)))
	if !ts.Valid() {
		return "", fmt.Errorf("unknown tool state %q: valid values are %v", s, allToolStates)
	}
	return ts, nil
}

// String returns the string representation.
func (s ToolState) String() string {
	return string(s)
}

// ── PlatformName ──

// PlatformName identifies a code hosting platform.
type PlatformName string

// Known platform names.
const (
	PlatformGitHub    PlatformName = "github"
	PlatformGitLab    PlatformName = "gitlab"
	PlatformBitbucket PlatformName = "bitbucket"
)

var allPlatforms = []PlatformName{
	PlatformGitHub,
	PlatformGitLab,
	PlatformBitbucket,
}

// Valid reports whether p is a known platform name.
func (p PlatformName) Valid() bool {
	for _, v := range allPlatforms {
		if p == v {
			return true
		}
	}
	return false
}

// ParsePlatformName converts a raw string to a validated PlatformName.
func ParsePlatformName(s string) (PlatformName, error) {
	p := PlatformName(strings.ToLower(strings.TrimSpace(s)))
	if !p.Valid() {
		return "", fmt.Errorf("unknown platform %q: valid values are %v", s, allPlatforms)
	}
	return p, nil
}

// String returns the string representation.
func (p PlatformName) String() string {
	return string(p)
}

// ── SessionType ──

// DefaultSessionTypes is the built-in list of session classification tags.
var DefaultSessionTypes = []string{
	"feature",
	"bug",
	"refactor",
	"exploration",
	"review",
	"devops",
	"other",
}

// ValidSessionType reports whether t is a known default session type.
func ValidSessionType(t string) bool {
	for _, st := range DefaultSessionTypes {
		if st == t {
			return true
		}
	}
	return false
}

// ── ProjectCategory ──

// DefaultProjectCategories is the built-in list of project classification categories.
var DefaultProjectCategories = []string{
	"backend",
	"frontend",
	"fullstack",
	"ops",
	"data",
	"mobile",
	"library",
	"docs",
}

// ValidProjectCategory reports whether c is a known default project category.
func ValidProjectCategory(c string) bool {
	for _, pc := range DefaultProjectCategories {
		if pc == c {
			return true
		}
	}
	return false
}

// ── ID ──

// ID is a unique identifier for a session.
type ID string

// NewID generates a new random session ID.
func NewID() ID {
	return ID(uuid.New().String())
}

// ParseID validates and returns an ID from a raw string.
func ParseID(s string) (ID, error) {
	if s == "" {
		return "", fmt.Errorf("session ID cannot be empty")
	}
	return ID(s), nil
}

// String returns the string representation.
func (id ID) String() string {
	return string(id)
}
