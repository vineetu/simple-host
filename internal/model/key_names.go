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
// (internal/auth/scope.go holds the route table).
const (
	KeyScopeFull   = "full"
	KeyScopeDeploy = "deploy"
)

// Why a stored key no longer works (APIKey.Expired).
const (
	KeyExpiredFixed = "expired" // past the expiry chosen when it was minted
	KeyExpiredIdle  = "idle"    // unused for KEY_IDLE_EXPIRY_DAYS
)
