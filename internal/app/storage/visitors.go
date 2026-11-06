package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	repository "github.com/vsriram/simple-host/internal/repository/storage"
)

// VisitorsInput identifies the site's requested visitors, by id, by email,
// or both; each list is resolved independently and the results concatenated
// in that order.
type VisitorsInput struct {
	Call   Call
	IDs    []string
	Emails []string
}

// Visitor is a resolved visitor identity.
type Visitor struct {
	VisitorID, Email string
	Found            bool
}

// Visitors resolves identities only for visitors who signed in on the site:
// ids to the email each one signed in with, emails to the visitor_id they
// signed in as. An id or email that never signed in on this site answers
// Found: false, whatever account it otherwise names.
func (s *Service) Visitors(ctx context.Context, in VisitorsInput) ([]Visitor, error) {
	if !in.Call.Owner && !in.Call.OwnerOnSite {
		return nil, &Error{Code: "forbidden", Message: "owner credential required"}
	}
	wrote, err := repository.SignedInAmong(ctx, s.db, in.Call.SiteID, in.IDs)
	if err != nil {
		return nil, err
	}
	out := make([]Visitor, 0, len(in.IDs)+len(in.Emails))
	for _, id := range in.IDs {
		v := Visitor{VisitorID: id}
		if wrote[id] {
			email, err := s.deps.VisitorEmail(ctx, id)
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
		id, _, err := s.resolveSignedInVisitorByEmail(ctx, in.Call.SiteID, email)
		if err != nil && !errors.Is(err, errAmbiguousVisitorEmail) {
			return nil, &Error{Code: "internal_error", Message: "internal server error"}
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

// errAmbiguousVisitorEmail: more than one person signed in on this site
// currently resolves (visitorEmail) to the same email. Rare (two accounts
// that both set the same self-signup username, or two OAuth identities
// sharing an address), but a write path must refuse rather than guess.
var errAmbiguousVisitorEmail = errors.New("ambiguous visitor email")

// resolveSignedInVisitorByEmail finds the one person, among those the
// server has recorded as signed in on this site, whose current canonical
// sign-in email (resolved the same way s.deps.VisitorEmail/
// storage_visitor_emails does for the forward direction — not just a raw
// username or OAuth-identity row match, which CandidateVisitorIDsForEmail
// alone would be) case-insensitively equals email. "" is returned if the
// server records no match; errAmbiguousVisitorEmail if more than one
// signed-in candidate's canonical email matches (never guesses between
// them).
func (s *Service) resolveSignedInVisitorByEmail(ctx context.Context, siteID, email string) (string, bool, error) {
	candidates, err := repository.CandidateVisitorIDsForEmail(ctx, s.db, siteID, email)
	if err != nil {
		return "", false, err
	}
	match := ""
	for _, id := range candidates {
		canonical, err := s.deps.VisitorEmail(ctx, id)
		if err != nil || canonical == "" {
			continue // gone, or this id does not resolve forward; not a match
		}
		if !strings.EqualFold(canonical, email) {
			continue // a stale username/OAuth row, not their current email
		}
		if match != "" && match != id {
			return "", false, errAmbiguousVisitorEmail
		}
		match = id
	}
	return match, match != "", nil
}

// ResolveOwnerFor validates and resolves the visitor a row is being added
// for, from a visitor_id or the email they signed in with: the general
// mechanism behind "owner adds a row on behalf of a signed-in visitor"
// (any reference, never a client or coach name). Only the owner (API key,
// connector token, or signed in on the site's own address) may resolve one,
// and only a visitor who has signed in on this site at least once resolves
// — there is no pre-sign-in email binding. ref == "" is not on-behalf-of
// and resolves to "", nil.
func (s *Service) ResolveOwnerFor(ctx context.Context, c Call, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	if !c.Owner && !c.OwnerOnSite {
		return "", &Error{Code: "forbidden", Message: "only the owner may add a row for someone else"}
	}
	if strings.Contains(ref, "@") {
		id, found, err := s.resolveSignedInVisitorByEmail(ctx, c.SiteID, strings.ToLower(ref))
		if errors.Is(err, errAmbiguousVisitorEmail) {
			return "", &Error{Code: "visitor_email_ambiguous", Message: "more than one person on this site currently signs in with that email; use their visitor_id instead"}
		}
		if err != nil {
			return "", &Error{Code: "internal_error", Message: "internal server error"}
		}
		if !found {
			return "", &Error{Code: "visitor_unknown", Message: "this person has not signed in on this site yet"}
		}
		return id, nil
	}
	wrote, err := repository.SignedInAmong(ctx, s.db, c.SiteID, []string{ref})
	if err != nil {
		return "", &Error{Code: "internal_error", Message: "internal server error"}
	}
	if !wrote[ref] {
		return "", &Error{Code: "visitor_unknown", Message: "this person has not signed in on this site yet"}
	}
	return ref, nil
}
