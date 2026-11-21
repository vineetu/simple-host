// Package oauth talks to Google and optional OAuth providers for sign-in.
// HTTP handlers do not own userinfo parsing or token exchange.
package oauth

import (
	"context"
)

// Identity is the stable provider user id plus a verified-email snapshot.
// Email is used only for the first-link decision; re-logins key on UserID.
type Identity struct {
	Provider      string
	UserID        string
	Email         string
	EmailVerified bool
}

// Provider is one OAuth authorization-code + PKCE S256 integration.
type Provider interface {
	Name() string
	AuthCodeURL(state, verifier string) string
	Exchange(ctx context.Context, code, verifier string) (Identity, error)
}
