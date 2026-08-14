package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"golang.org/x/oauth2"
)

const (
	githubAuthURL     = "https://github.com/login/oauth/authorize"
	githubTokenURL    = "https://github.com/login/oauth/access_token"
	githubUserInfoURL = "https://api.github.com/user"
)

// GitHub is the GitHub OAuth provider (read:user; no email scope).
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
			Scopes:       []string{"read:user"},
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubUserInfoURL, nil)
	if err != nil {
		return Identity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "simple-host-visitor-oauth")
	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("github userinfo: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Identity{}, fmt.Errorf("github userinfo: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("github userinfo: status %d", resp.StatusCode)
	}
	return ParseGitHubUserInfo(body)
}

// ParseGitHubUserInfo extracts the numeric id from a GitHub /user JSON body
// and returns it as decimal text.
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
