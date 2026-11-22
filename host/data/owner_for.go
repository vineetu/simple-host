package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	accountapp "github.com/vsriram/simple-host/host/access"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	repository "github.com/vsriram/simple-host/host/data/repo"
)

// errAmbiguousVisitorEmail: more than one person signed in on this site
// currently resolves (visitorEmail) to the same email. Rare (two accounts
// that both set the same self-signup username, or two OAuth identities
// sharing an address), but a write path must refuse rather than guess.
var ErrAmbiguousVisitorEmail = errors.New("ambiguous visitor email")

// resolveSignedInVisitorByEmail finds the one person, among those the
// server has recorded as signed in on this site, whose current canonical
// sign-in email (resolved the same way accountapp.VisitorEmail/
// storage_visitor_emails does for the forward direction — not just a raw
// username or OAuth-identity row match, which CandidateVisitorIDsForEmail
// alone would be) case-insensitively equals email. "" is returned if the
// server records no match; errAmbiguousVisitorEmail if more than one
// signed-in candidate's canonical email matches (never guesses between
// them).
func (s *Service) ResolveSignedInVisitorByEmail(ctx context.Context, siteID, email string) (string, bool, error) {
	candidates, err := accountsrepo.CandidateVisitorIDsForEmail(ctx, s.DB, siteID, email)
	if err != nil {
		return "", false, err
	}
	match := ""
	for _, id := range candidates {
		canonical, err := accountapp.VisitorEmail(ctx, s.DB, id)
		if err != nil || canonical == "" {
			continue // gone, or this id does not resolve forward; not a match
		}
		if !strings.EqualFold(canonical, email) {
			continue // a stale username/OAuth row, not their current email
		}
		if match != "" && match != id {
			return "", false, ErrAmbiguousVisitorEmail
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
		id, found, err := s.ResolveSignedInVisitorByEmail(ctx, c.SiteID, strings.ToLower(ref))
		if errors.Is(err, ErrAmbiguousVisitorEmail) {
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
	wrote, err := accountsrepo.SignedInAmong(ctx, s.DB, c.SiteID, []string{ref})
	if err != nil {
		return "", &Error{Code: "internal_error", Message: "internal server error"}
	}
	if !wrote[ref] {
		return "", &Error{Code: "visitor_unknown", Message: "this person has not signed in on this site yet"}
	}
	return ref, nil
}

// VisitorToolWriteInput contains the actor, target and validated write action.
type VisitorToolWriteInput struct {
	SiteID, UserID, App, Action, Resource, Kind string
	Cap                                         int
}

// CountVisitorToolWrite counts a visitor tool's write attempt before it runs,
// refusing once the day's cap is reached. Attempts count too, so failures and
// concurrent retries cannot evade the cap.
func (s *Service) CountVisitorToolWrite(ctx context.Context, in VisitorToolWriteInput) error {
	err := repository.CountVisitorAIWrite(ctx, s.DB, in.UserID, in.SiteID, in.Cap)
	if err == sql.ErrNoRows {
		return &Error{Code: "visitor_write_limit", Message: fmt.Sprintf("visitor_write_limit: daily limit of %d writes on this site reached; try again tomorrow (UTC)", in.Cap)}
	}
	return err
}

// RecordVisitorToolWrite marks a written entry as the visitor's own and
// records the write's app label in data history.
func (s *Service) RecordVisitorToolWrite(ctx context.Context, in VisitorToolWriteInput, result any) any {
	if entry, ok := result.(map[string]any); ok {
		entry["mine"] = true
	}
	// A history marker has no earlier value and therefore cannot be restored.
	err := repository.RecordVisitorAIWrite(ctx, s.DB, in.SiteID, in.Resource, in.Action+" "+in.Kind, in.UserID, in.App)
	if err != nil {
		return map[string]any{"entry": result, "note": "Saved, but the app label could not be recorded in data history."}
	}
	return result
}

// PruneVisitorAIWrites deletes the write records RecordVisitorToolWrite keeps once
// they are 30 days old, as the privacy page promises.
func PruneVisitorAIWrites(ctx context.Context, db *sql.DB) (int64, error) {
	return repository.PruneVisitorAIWrites(ctx, db)
}
