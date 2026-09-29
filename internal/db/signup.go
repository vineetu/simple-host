package db

import (
	"context"
	"strings"
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
}

const (
	SignupWebsite         = "website"
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
	_, err := q.ExecContext(ctx, `
		UPDATE users SET signup_source = $2, signup_agent = NULLIF($3, ''), signup_method = NULLIF($4, '')
		 WHERE id = $1 AND signup_source IS NULL`,
		userID, src, clip(s.Agent), clip(s.Method))
	return err
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > signupFieldMax {
		s = string(r[:signupFieldMax])
	}
	return s
}
