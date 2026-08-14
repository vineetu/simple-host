// Package oauth talks to Google and GitHub for visitor sign-in.
// HTTP handlers do not own userinfo parsing or token exchange.
package oauth

import (
	"context"
	"strings"
)

// Identity is the stable provider user id. No email.
type Identity struct {
	Provider string
	UserID   string
}

// Provider is one OAuth authorization-code + PKCE S256 integration.
type Provider interface {
	Name() string
	AuthCodeURL(state, verifier string) string
	Exchange(ctx context.Context, code, verifier string) (Identity, error)
}

// RedirectURI is {PUBLIC_BASE_URL}/v1/auth/oauth/{name}/callback.
func RedirectURI(publicBaseURL, name string) string {
	return strings.TrimRight(publicBaseURL, "/") + "/v1/auth/oauth/" + name + "/callback"
}
