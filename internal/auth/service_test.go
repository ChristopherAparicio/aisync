package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ChristopherAparicio/aisync/internal/auth"
)

// ── Mock AuthStore ──

type mockAuthStore struct {
	users  map[string]*auth.User   // keyed by ID
	byName map[string]*auth.User   // keyed by username
	keys   map[string]*auth.APIKey // keyed by ID
	byHash map[string]*auth.APIKey // keyed by key_hash
	byUser map[string][]*auth.APIKey
}

func newMockAuthStore() *mockAuthStore {
	return &mockAuthStore{
		users:  make(map[string]*auth.User),
		byName: make(map[string]*auth.User),
		keys:   make(map[string]*auth.APIKey),
		byHash: make(map[string]*auth.APIKey),
		byUser: make(map[string][]*auth.APIKey),
	}
}

func (m *mockAuthStore) CreateAuthUser(user *auth.User) error {
	if _, ok := m.byName[user.Username]; ok {
		return auth.ErrUserExists
	}
	m.users[user.ID] = user
	m.byName[user.Username] = user
	return nil
}

func (m *mockAuthStore) GetAuthUser(id string) (*auth.User, error) {
	u, ok := m.users[id]
	if !ok {
		return nil, auth.ErrUserNotFound
	}
	return u, nil
}

func (m *mockAuthStore) GetAuthUserByUsername(username string) (*auth.User, error) {
	u, ok := m.byName[username]
	if !ok {
		return nil, auth.ErrUserNotFound
	}
	return u, nil
}

func (m *mockAuthStore) UpdateAuthUser(user *auth.User) error {
	if _, ok := m.users[user.ID]; !ok {
		return auth.ErrUserNotFound
	}
	m.users[user.ID] = user
	m.byName[user.Username] = user
	return nil
}

func (m *mockAuthStore) ListAuthUsers() ([]*auth.User, error) {
	var result []*auth.User
	for _, u := range m.users {
		result = append(result, u)
	}
	return result, nil
}

func (m *mockAuthStore) CountAuthUsers() (int, error) {
	return len(m.users), nil
}

func (m *mockAuthStore) CreateAPIKey(key *auth.APIKey) error {
	m.keys[key.ID] = key
	m.byHash[key.KeyHash] = key
	m.byUser[key.UserID] = append(m.byUser[key.UserID], key)
	return nil
}

func (m *mockAuthStore) GetAPIKeyByHash(hash string) (*auth.APIKey, error) {
	k, ok := m.byHash[hash]
	if !ok {
		return nil, auth.ErrAPIKeyNotFound
	}
	return k, nil
}

func (m *mockAuthStore) ListAPIKeysByUser(userID string) ([]*auth.APIKey, error) {
	return m.byUser[userID], nil
}

func (m *mockAuthStore) UpdateAPIKey(key *auth.APIKey) error {
	if _, ok := m.keys[key.ID]; !ok {
		return auth.ErrAPIKeyNotFound
	}
	m.keys[key.ID] = key
	m.byHash[key.KeyHash] = key
	return nil
}

func (m *mockAuthStore) DeleteAPIKey(id string) error {
	k, ok := m.keys[id]
	if !ok {
		return auth.ErrAPIKeyNotFound
	}
	delete(m.keys, id)
	delete(m.byHash, k.KeyHash)
	// Clean up byUser list.
	userKeys := m.byUser[k.UserID]
	for i, uk := range userKeys {
		if uk.ID == id {
			m.byUser[k.UserID] = append(userKeys[:i], userKeys[i+1:]...)
			break
		}
	}
	return nil
}

// ── Helper ──

func newTestService(t *testing.T) (*auth.Service, *mockAuthStore) {
	t.Helper()
	store := newMockAuthStore()
	svc := auth.NewService(auth.ServiceConfig{
		Store:     store,
		JWTSecret: "test-secret-key-at-least-32-bytes!",
		TokenTTL:  1 * time.Hour,
	})
	return svc, store
}

// ── Register ──

func TestService_Register(t *testing.T) {
	svc, _ := newTestService(t)

	result, err := svc.Register(context.Background(), auth.RegisterRequest{
		Username: "alice",
		Password: "secure-password-123",
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if result.User.Username != "alice" {
		t.Errorf("Username = %q, want %q", result.User.Username, "alice")
	}
	// First user should be admin.
	if result.User.Role != auth.RoleAdmin {
		t.Errorf("first user Role = %q, want admin", result.User.Role)
	}
	if result.Token == "" {
		t.Error("Token should not be empty")
	}
}

func TestService_Register_SecondUserIsUser(t *testing.T) {
	svc, _ := newTestService(t)

	// First user → admin.
	_, _ = svc.Register(context.Background(), auth.RegisterRequest{
		Username: "admin",
		Password: "password-123456",
	})

	// Second user → user role.
	result, err := svc.Register(context.Background(), auth.RegisterRequest{
		Username: "bob",
		Password: "password-123456",
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if result.User.Role != auth.RoleUser {
		t.Errorf("second user Role = %q, want user", result.User.Role)
	}
}

func TestService_Register_Duplicate(t *testing.T) {
	svc, _ := newTestService(t)

	_, _ = svc.Register(context.Background(), auth.RegisterRequest{
		Username: "alice",
		Password: "password-123456",
	})

	_, err := svc.Register(context.Background(), auth.RegisterRequest{
		Username: "alice",
		Password: "different-pass-123",
	})
	if !errors.Is(err, auth.ErrUserExists) {
		t.Fatalf("Register() error = %v, want ErrUserExists", err)
	}
}

func TestService_Register_ValidationErrors(t *testing.T) {
	svc, _ := newTestService(t)

	tests := []struct {
		name     string
		username string
		password string
	}{
		{"empty username", "", "password-123456"},
		{"short password", "alice", "short"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Register(context.Background(), auth.RegisterRequest{
				Username: tt.username,
				Password: tt.password,
			})
			if err == nil {
				t.Error("Register() should return error")
			}
		})
	}
}

// ── Login ──

func TestService_Login(t *testing.T) {
	svc, _ := newTestService(t)

	_, _ = svc.Register(context.Background(), auth.RegisterRequest{
		Username: "alice",
		Password: "correct-password-123",
	})

	result, err := svc.Login(context.Background(), auth.LoginRequest{
		Username: "alice",
		Password: "correct-password-123",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if result.Token == "" {
		t.Error("Token should not be empty")
	}
	if result.User.Username != "alice" {
		t.Errorf("Username = %q, want %q", result.User.Username, "alice")
	}
}

func TestService_Login_WrongPassword(t *testing.T) {
	svc, _ := newTestService(t)

	_, _ = svc.Register(context.Background(), auth.RegisterRequest{
		Username: "alice",
		Password: "correct-password-123",
	})

	_, err := svc.Login(context.Background(), auth.LoginRequest{
		Username: "alice",
		Password: "wrong-password",
	})
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
	}
}

func TestService_Login_UserNotFound(t *testing.T) {
	svc, _ := newTestService(t)

	_, err := svc.Login(context.Background(), auth.LoginRequest{
		Username: "nobody",
		Password: "password-123456",
	})
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
	}
}

func TestService_Login_InactiveUser(t *testing.T) {
	svc, store := newTestService(t)

	reg, _ := svc.Register(context.Background(), auth.RegisterRequest{
		Username: "alice",
		Password: "password-123456",
	})

	// Deactivate the user.
	u := reg.User
	u.Active = false
	_ = store.UpdateAuthUser(u)

	_, err := svc.Login(context.Background(), auth.LoginRequest{
		Username: "alice",
		Password: "password-123456",
	})
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
	}
}

// ── ValidateToken ──

func TestService_ValidateToken(t *testing.T) {
	svc, _ := newTestService(t)

	reg, _ := svc.Register(context.Background(), auth.RegisterRequest{
		Username: "alice",
		Password: "password-123456",
	})

	claims, err := svc.ValidateToken(context.Background(), reg.Token)
	if err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}
	if claims.Username != "alice" {
		t.Errorf("Username = %q, want %q", claims.Username, "alice")
	}
}

func TestService_ValidateToken_Invalid(t *testing.T) {
	svc, _ := newTestService(t)

	_, err := svc.ValidateToken(context.Background(), "garbage-token")
	if err == nil {
		t.Error("ValidateToken() should return error for invalid token")
	}
}

// ── API Key Management ──

func TestService_CreateAPIKey(t *testing.T) {
	svc, _ := newTestService(t)

	reg, _ := svc.Register(context.Background(), auth.RegisterRequest{
		Username: "alice",
		Password: "password-123456",
	})

	result, err := svc.CreateAPIKey(context.Background(), auth.CreateAPIKeyRequest{
		UserID: reg.User.ID,
		Name:   "CI Pipeline",
	})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	if result.RawKey == "" {
		t.Error("RawKey should not be empty")
	}
	if result.APIKey.Name != "CI Pipeline" {
		t.Errorf("Name = %q, want %q", result.APIKey.Name, "CI Pipeline")
	}
}

func TestService_ValidateAPIKey(t *testing.T) {
	svc, _ := newTestService(t)

	reg, _ := svc.Register(context.Background(), auth.RegisterRequest{
		Username: "alice",
		Password: "password-123456",
	})

	keyResult, _ := svc.CreateAPIKey(context.Background(), auth.CreateAPIKeyRequest{
		UserID: reg.User.ID,
		Name:   "test",
	})

	claims, err := svc.ValidateAPIKey(context.Background(), keyResult.RawKey)
	if err != nil {
		t.Fatalf("ValidateAPIKey() error = %v", err)
	}
	if claims.UserID != reg.User.ID {
		t.Errorf("UserID = %q, want %q", claims.UserID, reg.User.ID)
	}
	if claims.Source != "api_key" {
		t.Errorf("Source = %q, want %q", claims.Source, "api_key")
	}
}

func TestService_ValidateAPIKey_Revoked(t *testing.T) {
	svc, _ := newTestService(t)

	reg, _ := svc.Register(context.Background(), auth.RegisterRequest{
		Username: "alice",
		Password: "password-123456",
	})

	keyResult, _ := svc.CreateAPIKey(context.Background(), auth.CreateAPIKeyRequest{
		UserID: reg.User.ID,
		Name:   "revoke-me",
	})

	// Revoke the key.
	_ = svc.RevokeAPIKey(context.Background(), keyResult.APIKey.ID, reg.User.ID)

	_, err := svc.ValidateAPIKey(context.Background(), keyResult.RawKey)
	if !errors.Is(err, auth.ErrAPIKeyRevoked) {
		t.Fatalf("ValidateAPIKey() error = %v, want ErrAPIKeyRevoked", err)
	}
}

func TestService_ListAPIKeys(t *testing.T) {
	svc, _ := newTestService(t)

	reg, _ := svc.Register(context.Background(), auth.RegisterRequest{
		Username: "alice",
		Password: "password-123456",
	})

	_, _ = svc.CreateAPIKey(context.Background(), auth.CreateAPIKeyRequest{UserID: reg.User.ID, Name: "key1"})
	_, _ = svc.CreateAPIKey(context.Background(), auth.CreateAPIKeyRequest{UserID: reg.User.ID, Name: "key2"})

	keys, err := svc.ListAPIKeys(context.Background(), reg.User.ID)
	if err != nil {
		t.Fatalf("ListAPIKeys() error = %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}
}

func TestService_DeleteAPIKey(t *testing.T) {
	svc, _ := newTestService(t)

	reg, _ := svc.Register(context.Background(), auth.RegisterRequest{
		Username: "alice",
		Password: "password-123456",
	})

	keyResult, _ := svc.CreateAPIKey(context.Background(), auth.CreateAPIKeyRequest{UserID: reg.User.ID, Name: "delete-me"})

	err := svc.DeleteAPIKey(context.Background(), keyResult.APIKey.ID, reg.User.ID)
	if err != nil {
		t.Fatalf("DeleteAPIKey() error = %v", err)
	}

	keys, _ := svc.ListAPIKeys(context.Background(), reg.User.ID)
	if len(keys) != 0 {
		t.Errorf("expected 0 keys after delete, got %d", len(keys))
	}
}
