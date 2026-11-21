package servertest

import (
	"database/sql"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
)

func testOAuthSettings(cfg config.Config, service *accounts.Service) accesshttp.OAuthSettings {
	providers := make(map[string]accounts.Provider, 3)
	if cfg.GoogleOAuthEnabled() {
		providers["google"] = FakeProvider{}
	}
	if cfg.GitHubOAuthEnabled() {
		providers["github"] = FakeProvider{}
	}
	if cfg.CompanySignInEnabled() {
		providers["oidc"] = SSOOIDCProvider{}
	}
	return accesshttp.OAuthSettings{
		PublicBaseURL: cfg.PublicBaseURL, SiteDomain: cfg.SiteDomain, ContentHost: cfg.ContentHost, CNAMETarget: cfg.CNAMETarget, CustomDomainIP: cfg.CustomDomainIP, CustomDomainIPAlso: cfg.CustomDomainIPAlso, OIDCLabel: cfg.OIDCLabel, EmailEnabled: cfg.EmailSignInEnabled(),
		SignIn: accounts.NewProviderSignIn(service, providers),
	}
}
func NewTestOAuthHandler(database *sql.DB, service *accounts.Service, cfg config.Config, editions ...*edition.Edition) *accesshttp.OAuthHandler {
	return accesshttp.NewOAuthHandler(database, service, testOAuthSettings(cfg, service), editions...)
}
