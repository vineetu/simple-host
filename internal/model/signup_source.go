package model

const (
	SignupWebsite = "website"
	// SignupArtifactHub is a website sign-up by a visitor who arrived through
	// artifact-hub.com (INTENT 2026-10-15).
	SignupArtifactHub = "artifact-hub"
	// SignupVercelShip is a website sign-up by a visitor who arrived through
	// the post-poll link on ship.vineetsriram.com (Vercel Ship, 2026-10-15).
	SignupVercelShip = "vercel-ship"
	// SignupAgentCon is a website sign-up by a visitor who arrived through the
	// QR code and link given out at Agent Con, San Jose (2026-10-22).
	SignupAgentCon = "agentcon"
	// SignupFromPrefix marks a /?from= value with no name in the handler's
	// arrival source list: "from:<value>".
	SignupFromPrefix      = "from:"
	SignupConnectorPrefix = "connector:"
	SignupAgent           = "agent"
	SignupVisitor         = "visitor"
	SignupAdmin           = "admin"
	SignupReviewer        = "reviewer"

	SignupMethodEmail    = "email"
	SignupMethodGoogle   = "google"
	SignupMethodGitHub   = "github"
	SignupMethodIssued   = "issued"
	SignupMethodPassword = "password"
)
