package accesshttp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"net/url"

	accounts "github.com/vsriram/simple-host/host/access"
)

// testNonce is a sign-in nonce whose hash tests store directly.
const testNonce = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func testNonceHash(n string) sql.NullString {
	sum := sha256.Sum256([]byte(n))
	return sql.NullString{String: base64.RawURLEncoding.EncodeToString(sum[:]), Valid: true}
}

// fakeProvider signs in whoever the callback's code names.
type fakeProvider struct{}

func (fakeProvider) Name() string { return "google" }
func (fakeProvider) AuthCodeURL(state, _ string) string {
	return "https://accounts.example.test/auth?state=" + url.QueryEscape(state)
}
func (fakeProvider) Exchange(_ context.Context, code, _ string) (accounts.ProviderIdentity, error) {
	return accounts.ProviderIdentity{Provider: "google", UserID: "g-" + code, Email: code + "@example.test", EmailVerified: true}, nil
}
