package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/oauth2"
)

const (
	googleAuthURL     = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL    = "https://oauth2.googleapis.com/token"
	googleUserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
)

// Google is the Google OAuth provider (openid + profile; no email).
type Google struct {
	conf *oauth2.Config
}

// NewGoogle builds a Google provider. redirectURI must be the apex callback.
func NewGoogle(clientID, clientSecret, redirectURI string) *Google {
	return &Google{
		conf: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURI,
			Scopes:       []string{"openid", "profile"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  googleAuthURL,
				TokenURL: googleTokenURL,
			},
		},
	}
}

func (g *Google) Name() string { return "google" }

func (g *Google) AuthCodeURL(state, verifier string) string {
	return g.conf.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
}

func (g *Google) Exchange(ctx context.Context, code, verifier string) (Identity, error) {
	ctx, cancel := withOAuthTimeout(ctx)
	defer cancel()
	tok, err := g.conf.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, fmt.Errorf("google token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleUserInfoURL, nil)
	if err != nil {
		return Identity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("google userinfo: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Identity{}, fmt.Errorf("google userinfo: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("google userinfo: status %d", resp.StatusCode)
	}
	return ParseGoogleUserInfo(body)
}

// ParseGoogleUserInfo extracts sub from a Google userinfo JSON body.
func ParseGoogleUserInfo(body []byte) (Identity, error) {
	var payload struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Identity{}, fmt.Errorf("google userinfo: %w", err)
	}
	if payload.Sub == "" {
		return Identity{}, fmt.Errorf("google userinfo: missing sub")
	}
	return Identity{Provider: "google", UserID: payload.Sub}, nil
}

var oauthHTTPClient = &http.Client{Timeout: 10 * time.Second}

func withOAuthTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, 10*time.Second)
}
