package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/oauth2"
)

const (
	githubAuthURL     = "https://github.com/login/oauth/authorize"
	githubTokenURL    = "https://github.com/login/oauth/access_token"
	githubUserInfoURL = "https://api.github.com/user"
	githubEmailsURL   = "https://api.github.com/user/emails"
)

// GitHub is the GitHub OAuth provider (read:user + user:email).
type GitHub struct {
	conf *oauth2.Config
}

// NewGitHub builds a GitHub provider. redirectURI must be the apex callback.
func NewGitHub(clientID, clientSecret, redirectURI string) *GitHub {
	return &GitHub{
		conf: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURI,
			Scopes:       []string{"read:user", "user:email"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  githubAuthURL,
				TokenURL: githubTokenURL,
			},
		},
	}
}

func (g *GitHub) Name() string { return "github" }

func (g *GitHub) AuthCodeURL(state, verifier string) string {
	return g.conf.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
}

func (g *GitHub) Exchange(ctx context.Context, code, verifier string) (Identity, error) {
	ctx, cancel := withOAuthTimeout(ctx)
	defer cancel()
	tok, err := g.conf.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, fmt.Errorf("github token: %w", err)
	}
	ident, err := githubGETIdentity(ctx, tok.AccessToken)
	if err != nil {
		return Identity{}, err
	}
	email, verified, err := githubGETPrimaryEmail(ctx, tok.AccessToken)
	if err != nil {
		return Identity{}, err
	}
	ident.Email = email
	ident.EmailVerified = verified
	return ident, nil
}

func githubGETIdentity(ctx context.Context, accessToken string) (Identity, error) {
	body, err := githubGET(ctx, githubUserInfoURL, accessToken)
	if err != nil {
		return Identity{}, fmt.Errorf("github userinfo: %w", err)
	}
	return ParseGitHubUserInfo(body)
}

func githubGETPrimaryEmail(ctx context.Context, accessToken string) (string, bool, error) {
	body, err := githubGET(ctx, githubEmailsURL, accessToken)
	if err != nil {
		return "", false, fmt.Errorf("github emails: %w", err)
	}
	email, verified := ParseGitHubEmails(body)
	return email, verified, nil
}

func githubGET(ctx context.Context, url, accessToken string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "simple-host-visitor-oauth")
	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return body, nil
}

// ParseGitHubUserInfo extracts the numeric id from a GitHub /user JSON body
// and returns it as decimal text. The /user email field is ignored.
func ParseGitHubUserInfo(body []byte) (Identity, error) {
	var payload struct {
		ID *int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Identity{}, fmt.Errorf("github userinfo: %w", err)
	}
	if payload.ID == nil {
		return Identity{}, fmt.Errorf("github userinfo: missing id")
	}
	return Identity{Provider: "github", UserID: strconv.FormatInt(*payload.ID, 10)}, nil
}

// ParseGitHubEmails picks the first primary && verified address from a
// GitHub /user/emails JSON body, lowercased. If none, email is empty and
// verified is false — do not trust GET /user's email field.
func ParseGitHubEmails(body []byte) (email string, verified bool) {
	var list []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return "", false
	}
	for _, e := range list {
		if e.Primary && e.Verified {
			got := strings.ToLower(strings.TrimSpace(e.Email))
			if got != "" {
				return got, true
			}
		}
	}
	return "", false
}
