package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/vsriram/simple-host/internal/model"
)

// Signup is where an account came from, recorded once when it is created
// (v075-signup-source.sql). Source is one of the Signup* values, or
// SignupConnectorPrefix followed by the connected app's name; Agent names the
// agent for SignupAgent; Method is how the person proved who they are.
// Nothing here is ever an IP address.
type Signup = model.Signup

// SignupDetail contains same-visit URL/form attribution: from, utm_* and
// the linking host only, recorded at account creation. Never an IP address.
type SignupDetail = model.SignupDetail

const (
	SignupWebsite         = model.SignupWebsite
	SignupArtifactHub     = model.SignupArtifactHub
	SignupVercelShip      = model.SignupVercelShip
	SignupAgentCon        = model.SignupAgentCon
	SignupFromPrefix      = model.SignupFromPrefix
	SignupConnectorPrefix = model.SignupConnectorPrefix
	SignupAgent           = model.SignupAgent
	SignupVisitor         = model.SignupVisitor
	SignupAdmin           = model.SignupAdmin
	SignupReviewer        = model.SignupReviewer
	SignupMethodEmail     = model.SignupMethodEmail
	SignupMethodGoogle    = model.SignupMethodGoogle
	SignupMethodGitHub    = model.SignupMethodGitHub
	SignupMethodIssued    = model.SignupMethodIssued
	SignupMethodPassword  = model.SignupMethodPassword
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
