package server

import (
	"context"

	"github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/internal/oauth"
)

// OAuthSettings adapts configured sign-in providers for the HTTP transport.
func OAuthSettings(cfg config.Config, service *accounts.Service) handler.OAuthSettings {
	return handler.OAuthSettings{
		PublicBaseURL: cfg.PublicBaseURL, SiteDomain: cfg.SiteDomain, ContentHost: cfg.ContentHost, CNAMETarget: cfg.CNAMETarget, CustomDomainIP: cfg.CustomDomainIP, CustomDomainIPAlso: cfg.CustomDomainIPAlso, OIDCLabel: cfg.OIDCLabel, EmailEnabled: cfg.EmailSignInEnabled(), PasswordEnabled: cfg.PasswordSignIn == "on", PasswordSelfSignup: cfg.PasswordSelfSignup != "off",
		SignIn: configuredProviderSignIn(service, cfg),
	}
}

// signInProvider adapts provider snapshots to the accounts consumer contract.
type signInProvider struct{ oauth.Provider }

func (p signInProvider) Exchange(ctx context.Context, code, verifier string) (accounts.ProviderIdentity, error) {
	ident, err := p.Provider.Exchange(ctx, code, verifier)
	return accounts.ProviderIdentity{
		Provider: ident.Provider, UserID: ident.UserID,
		Email: ident.Email, EmailVerified: ident.EmailVerified,
	}, err
}

func configuredProviderSignIn(service *accounts.Service, cfg config.Config) *accounts.ProviderSignIn {
	providers := make(map[string]accounts.Provider, 3)
	if cfg.GoogleOAuthEnabled() {
		providers["google"] = signInProvider{oauth.NewGoogle(cfg.GoogleOAuthClientID, cfg.GoogleOAuthClientSecret, cfg.OAuthRedirectURI("google"))}
	}
	if cfg.GitHubOAuthEnabled() {
		providers["github"] = signInProvider{oauth.NewGitHub(cfg.GitHubOAuthClientID, cfg.GitHubOAuthClientSecret, cfg.OAuthRedirectURI("github"))}
	}
	if cfg.CompanySignInEnabled() {
		providers["oidc"] = signInProvider{oauth.NewCompany(oauth.CompanyConfig{
			Issuer: cfg.OIDCIssuer, ClientID: cfg.OIDCClientID, ClientSecret: cfg.OIDCClientSecret,
			RedirectURI: cfg.OAuthRedirectURI("oidc"), AllowedEmailDomains: cfg.AllowedEmailDomains,
			InsecureAllowed: cfg.OIDCInsecureAllowed,
		})}
	}
	return accounts.NewProviderSignIn(service, providers)
}
