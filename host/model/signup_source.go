package model

const (
	SignupWebsite = "website"
	// SignupArtifactHub is a website sign-up by a visitor who arrived through
	// artifact-hub.com.
	SignupArtifactHub = "artifact-hub"
	// SignupVercelShip is a website sign-up by a visitor who arrived through
	// the post-poll link on ship.vineetsriram.com (Vercel Ship).
	SignupVercelShip = "vercel-ship"
	// SignupAgentCon is a website sign-up by a visitor who arrived through the
	// QR code and link given out at Agent Con, San Jose.
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
	SignupMethodPassword = "password"
)
