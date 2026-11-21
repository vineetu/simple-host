package accesstest

import (
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/server/servertest"
)

// setConnectorAppLimits is setAppLimits for the connectorApp fixture.
func setConnectorAppLimits(a *servertest.ConnectorApp, l config.Limits) {
	a.Sites.Limits, a.Access.Limits, a.Data.Limits = l, l, l
	a.Conn.Limits = l
	a.Users.Limits = l
}
