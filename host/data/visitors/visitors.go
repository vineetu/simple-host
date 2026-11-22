package visitors

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/vsriram/simple-host/host/data"

	accountapp "github.com/vsriram/simple-host/host/access"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
)

// VisitorsInput identifies the site's requested visitors, by id, by email,
// or both, as the request sent them (VisitorRefs reads them); each list is
// resolved independently and the results concatenated in that order.
type VisitorsInput struct {
	Call   data.Call
	IDs    []string
	Emails []string
}

// Visitor is a resolved visitor identity. Found false: the id never signed
// in on this site, or its account is gone.
type Visitor struct {
	VisitorID string `json:"visitor_id"`
	Email     string `json:"email,omitempty"`
	Found     bool   `json:"found"`
}

// Visitors resolves identities only for visitors who signed in on the site:
// ids to the email each one signed in with, emails to the visitor_id they
// signed in as. An id or email that never signed in on this site answers
// Found: false, whatever account it otherwise names.
//
// Email resolution is not a Simple Hack concept, so a product's bindings
// answer as if the route did not exist.
func Visitors(s *data.Service, ctx context.Context, in VisitorsInput) ([]Visitor, error) {
	if s.Deps.Bindings != nil {
		return nil, &data.Error{Code: "not_found", Message: "storage route not found"}
	}
	if !in.Call.Owner && !in.Call.OwnerOnSite {
		return nil, &data.Error{Code: "forbidden", Message: "owner credential required"}
	}
	var err error
	if in.IDs, in.Emails, err = data.VisitorRefs(in.IDs, in.Emails); err != nil {
		return nil, err
	}
	wrote, err := accountsrepo.SignedInAmong(ctx, s.DB, in.Call.SiteID, in.IDs)
	if err != nil {
		return nil, err
	}
	out := make([]Visitor, 0, len(in.IDs)+len(in.Emails))
	for _, id := range in.IDs {
		v := Visitor{VisitorID: id}
		if wrote[id] {
			email, err := accountapp.VisitorEmail(ctx, s.DB, id)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			v.Email, v.Found = email, err == nil && email != ""
		}
		out = append(out, v)
	}
	for _, email := range in.Emails {
		email = strings.ToLower(strings.TrimSpace(email))
		v := Visitor{Email: email}
		id, _, err := s.ResolveSignedInVisitorByEmail(ctx, in.Call.SiteID, email)
		if err != nil && !errors.Is(err, data.ErrAmbiguousVisitorEmail) {
			return nil, &data.Error{Code: "internal_error", Message: "internal server error"}
		}
		// Ambiguous (more than one signed-in person currently uses this
		// email) is reported the same as not found: the bulk lookup's
		// {visitor_id, email, found} shape has no room to say which, and
		// guessing one of several people is worse than saying not found.
		v.VisitorID, v.Found = id, id != "" && err == nil
		out = append(out, v)
	}
	return out, nil
}
