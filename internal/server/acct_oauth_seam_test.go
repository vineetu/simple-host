package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/oauth"
)

type seamProvider struct {
	oauth.Provider
	exchange func(context.Context, string, string) (oauth.Identity, error)
}

func (p seamProvider) Exchange(ctx context.Context, code, verifier string) (oauth.Identity, error) {
	return p.exchange(ctx, code, verifier)
}

func TestSignInProviderExchange(t *testing.T) {
	refused := fmt.Errorf("provider: %w", &oauth.RefusalError{Reason: "email_domain", Message: "email domain is not allowed"})
	for _, exchangeErr := range []error{nil, refused} {
		p := signInProvider{seamProvider{exchange: func(ctx context.Context, code, verifier string) (oauth.Identity, error) {
			if ctx != t.Context() || code != "code" || verifier != "verifier" {
				t.Fatal("exchange arguments changed")
			}
			return oauth.Identity{Provider: "oidc", UserID: "subject", Email: "person@example.test", EmailVerified: true}, exchangeErr
		}}}
		got, err := p.Exchange(t.Context(), "code", "verifier")
		want := accounts.ProviderIdentity{Provider: "oidc", UserID: "subject", Email: "person@example.test", EmailVerified: true}
		if got != want || err != exchangeErr {
			t.Fatalf("exchange = %+v, %v; want %+v, %v", got, err, want, exchangeErr)
		}
	}
}
