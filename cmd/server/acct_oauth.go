package main

import (
	"github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/handler"
)

func oauthSettings(cfg config.Config, service *accounts.Service) handler.OAuthSettings {
	return handler.OAuthSettings{
		PublicBaseURL: cfg.PublicBaseURL, SiteDomain: cfg.SiteDomain, ContentHost: cfg.ContentHost, CNAMETarget: cfg.CNAMETarget, CustomDomainIP: cfg.CustomDomainIP, CustomDomainIPAlso: cfg.CustomDomainIPAlso, OIDCLabel: cfg.OIDCLabel, EmailEnabled: cfg.EmailSignInEnabled(),
		SignIn: accounts.NewConfiguredProviderSignIn(service, accounts.ProviderSettings{
			GoogleEnabled: cfg.GoogleOAuthEnabled(), GitHubEnabled: cfg.GitHubOAuthEnabled(), CompanyEnabled: cfg.CompanySignInEnabled(),
			GoogleOAuthClientID: cfg.GoogleOAuthClientID, GoogleOAuthClientSecret: cfg.GoogleOAuthClientSecret, GoogleRedirectURI: cfg.OAuthRedirectURI("google"),
			GitHubOAuthClientID: cfg.GitHubOAuthClientID, GitHubOAuthClientSecret: cfg.GitHubOAuthClientSecret, GithubRedirectURI: cfg.OAuthRedirectURI("github"),
			OIDCIssuer: cfg.OIDCIssuer, OIDCClientID: cfg.OIDCClientID, OIDCClientSecret: cfg.OIDCClientSecret, OidcRedirectURI: cfg.OAuthRedirectURI("oidc"), AllowedEmailDomains: cfg.AllowedEmailDomains, OIDCInsecureAllowed: cfg.OIDCInsecureAllowed,
		}),
	}
}
