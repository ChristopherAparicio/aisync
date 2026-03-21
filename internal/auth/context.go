package auth

import "context"

// contextKey is an unexported type for context keys to avoid collisions.
type contextKey struct{}

// claimsKey is the context key for storing authenticated Claims.
var claimsKey = contextKey{}

// WithClaims returns a new context with the given claims attached.
func WithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, claims)
}

// ClaimsFromContext extracts the authenticated claims from context.
// Returns nil if the request is unauthenticated.
func ClaimsFromContext(ctx context.Context) *Claims {
	claims, _ := ctx.Value(claimsKey).(*Claims)
	return claims
}

// MustClaimsFromContext extracts claims or panics.
// Use only in handlers that are known to be behind auth middleware.
func MustClaimsFromContext(ctx context.Context) *Claims {
	claims := ClaimsFromContext(ctx)
	if claims == nil {
		panic("auth: no claims in context — handler must be behind auth middleware")
	}
	return claims
}
