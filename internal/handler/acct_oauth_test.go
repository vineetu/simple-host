package handler

import (
	"database/sql"
	"github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/edition"
)

func testOAuthSettings(cfg config.Config, service *accounts.Service) OAuthSettings {
	providers := make(map[string]accounts.Provider, 3)
	if cfg.GoogleOAuthEnabled() {
		providers["google"] = fakeProvider{}
	}
	if cfg.GitHubOAuthEnabled() {
		providers["github"] = fakeProvider{}
	}
	if cfg.CompanySignInEnabled() {
		providers["oidc"] = ssoOIDCProvider{}
	}
	return OAuthSettings{
		PublicBaseURL: cfg.PublicBaseURL, SiteDomain: cfg.SiteDomain, ContentHost: cfg.ContentHost, CNAMETarget: cfg.CNAMETarget, CustomDomainIP: cfg.CustomDomainIP, CustomDomainIPAlso: cfg.CustomDomainIPAlso, OIDCLabel: cfg.OIDCLabel, EmailEnabled: cfg.EmailSignInEnabled(),
		SignIn: accounts.NewProviderSignIn(service, providers),
	}
}
func newTestOAuthHandler(database *sql.DB, service *accounts.Service, cfg config.Config, editions ...*edition.Edition) *OAuthHandler {
	return NewOAuthHandler(database, service, testOAuthSettings(cfg, service), editions...)
}
