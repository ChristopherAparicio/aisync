package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWTManager handles issuing and validating JWT tokens.
// It uses HMAC-SHA256 signing with a shared secret.
type JWTManager struct {
	secret []byte
	ttl    time.Duration
}

// NewJWTManager creates a JWT manager with the given secret and token TTL.
func NewJWTManager(secret string, ttl time.Duration) *JWTManager {
	return &JWTManager{
		secret: []byte(secret),
		ttl:    ttl,
	}
}

// jwtClaims wraps our Claims with the standard JWT registered claims.
type jwtClaims struct {
	UserID   string `json:"uid"`
	Username string `json:"sub"`
	Role     Role   `json:"role"`
	jwt.RegisteredClaims
}

// Issue creates a new JWT token for the given user.
func (m *JWTManager) Issue(user *User) (string, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(m.ttl)

	claims := jwtClaims{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			Issuer:    "aisync",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("signing JWT: %w", err)
	}

	return signed, nil
}

// Validate parses and validates a JWT token string.
// Returns the Claims if valid, or an error if expired/invalid.
func (m *JWTManager) Validate(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &jwtClaims{}, func(token *jwt.Token) (any, error) {
		// Ensure we're using HMAC.
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.secret, nil
	})
	if err != nil {
		return nil, ErrTokenInvalid
	}

	jc, ok := token.Claims.(*jwtClaims)
	if !ok || !token.Valid {
		return nil, ErrTokenInvalid
	}

	claims := &Claims{
		UserID:   jc.UserID,
		Username: jc.Username,
		Role:     jc.Role,
		Source:   "jwt",
	}

	if jc.IssuedAt != nil {
		claims.IssuedAt = jc.IssuedAt.Time
	}
	if jc.ExpiresAt != nil {
		claims.ExpiresAt = jc.ExpiresAt.Time
		if claims.IsExpired() {
			return nil, ErrTokenExpired
		}
	}

	return claims, nil
}

// ParseTokenUserID extracts the user_id from a JWT token WITHOUT signature
// verification. This is used by the CLI to resolve --me without needing the
// server's JWT secret. It only reads the "uid" claim from the payload.
//
// NOTE: This does NOT validate the token. Use Validate() for authorization.
func ParseTokenUserID(tokenStr string) (string, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid JWT format")
	}

	// Decode the payload (second part).
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decoding JWT payload: %w", err)
	}

	var claims struct {
		UserID string `json:"uid"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("parsing JWT claims: %w", err)
	}

	if claims.UserID == "" {
		return "", fmt.Errorf("no user_id in JWT token")
	}
	return claims.UserID, nil
}
