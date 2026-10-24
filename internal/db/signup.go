package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// Signup is where an account came from, recorded once when it is created
// (v075-signup-source.sql). Source is one of the Signup* values, or
// SignupConnectorPrefix followed by the connected app's name; Agent names the
// agent for SignupAgent; Method is how the person proved who they are.
// Nothing here is ever an IP address.
type Signup struct {
	Source   string
	Agent    string
	Method   string
	Inferred bool // backfilled from other rows, not recorded at the time
	// Detail is the outside link that brought the browser in, when the
	// account is credited to one (users.signup_detail, zf-signup-detail.sql).
	Detail *SignupDetail
}

// SignupDetail contains same-visit URL/form attribution: from, utm_* and
// the linking host only, recorded at account creation. Never an IP address.
type SignupDetail struct {
	From        string    `json:"from"`
	UTMSource   string    `json:"utm_source,omitempty"`
	UTMMedium   string    `json:"utm_medium,omitempty"`
	UTMCampaign string    `json:"utm_campaign,omitempty"`
	UTMTerm     string    `json:"utm_term,omitempty"`
	UTMContent  string    `json:"utm_content,omitempty"`
	Referrer    string    `json:"referrer,omitempty"`
	LandedAt    time.Time `json:"landed_at,omitzero"`
}

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

// signupFieldMax caps each stored field; the values come partly from the
// caller (an app's registered name, an agent's header), so they are bounded.
const signupFieldMax = 80

// SetSignup records where a just-created account came from. It writes only
// while the account has no source yet, so the first record wins and a later
// sign-in never rewrites it.
func SetSignup(ctx context.Context, q Querier, userID string, s Signup) error {
	src := clip(s.Source)
	if src == "" || userID == "" {
		return nil
	}
	var detail sql.NullString
	if s.Detail != nil {
		b, err := json.Marshal(s.Detail)
		if err != nil {
			return err
		}
		detail = sql.NullString{String: string(b), Valid: true}
	}
	_, err := q.ExecContext(ctx, `
		UPDATE users SET signup_source = $2, signup_agent = NULLIF($3, ''), signup_method = NULLIF($4, ''), signup_detail = $5::jsonb
		 WHERE id = $1 AND signup_source IS NULL`,
		userID, src, clip(s.Agent), clip(s.Method), detail)
	return err
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > signupFieldMax {
		s = string(r[:signupFieldMax])
	}
	return s
}
