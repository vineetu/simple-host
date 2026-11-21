package model

const (
	KeyNameDashboard = "dashboard sign-in"
	KeyNameAgent     = "agent sign-in"
	KeyNameEvent     = "event account"
	KeyNameReviewer  = "reviewer sign-in"
	KeyNameRotated   = "replacement key"
)

// Key scopes (api_keys.scope). A full key does everything the account can
// through the API; a deploy key only what a CI job that publishes needs
// (host/auth/scope.go holds the route table).
const (
	KeyScopeFull   = "full"
	KeyScopeDeploy = "deploy"
)
