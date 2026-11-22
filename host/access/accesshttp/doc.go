// Package accesshttp composes Access's routes from its features: the
// person's own routes (UserHandler: signin, profile, keys, teams), the site
// gate (AccessHandler, host/access/gate), the operator's and the account's
// data routes (RegisterAccountRoutes: operator, accountdata, suspension,
// profile.Patch), site editors (RegisterEditors) and the connector
// (host/access/connector). A route lives in its feature's package, beside
// its logic; this package only builds and mounts them and holds what
// several features are set from (limits, the reviewer, the sites).
package accesshttp
