package accesshttp

import (
	"database/sql"
	"net/http"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/access/connector"
	"github.com/vsriram/simple-host/host/edition"
)

// ConnectorHandler is the connector: the OAuth authorization server and /mcp
// (host/access/connector).
type ConnectorHandler = connector.Handler

// NewConnectorHandler builds the connector (connector.New); access gives its
// tools the visitor reference key notifications use. access and alerts may
// be nil.
func NewConnectorHandler(database *sql.DB, publicBaseURL, adminAPIKey, siteDomain, contentHost, skillVersion string, upstream http.Handler, hostedEvents bool, access *AccessHandler, alerts *auth.SignInAlerts, editions ...*edition.Edition) *ConnectorHandler {
	var key []byte
	if access != nil {
		key = access.VisitorRefKey()
	}
	return connector.New(database, publicBaseURL, adminAPIKey, siteDomain, contentHost, skillVersion, upstream, hostedEvents, key, alerts, editions...)
}

// HashReviewerPassword is connector.HashReviewerPassword.
func HashReviewerPassword(password string) (string, error) {
	return connector.HashReviewerPassword(password)
}
