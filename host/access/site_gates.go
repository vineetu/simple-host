package access

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/vsriram/simple-host/host/access/repo"
	authrules "github.com/vsriram/simple-host/host/access/rules"
	"github.com/vsriram/simple-host/host/model"
)

// Refusal is a request Access turns down: the HTTP status, the stable code
// and the message the caller is shown.
type Refusal struct {
	Status        int
	Code, Message string
}

func (e *Refusal) Error() string { return e.Message }

func conflict(code, msg string) *Refusal { return &Refusal{http.StatusConflict, code, msg} }

// GateSite is what the passcode and named-viewers rules read about a site
// and this server.
type GateSite struct {
	// OwnAddress: the site is served on an address of its own (never on the
	// one every site shares).
	OwnAddress bool
	// EventSites: the Simple Hack platform, where sites have their own rules.
	EventSites bool
	// NoVisitorSignIn: this server has no way for a visitor to sign in.
	NoVisitorSignIn bool
}

// PasscodeRefusal is why the site cannot take a passcode, nil when it can.
// A site has named viewers or a passcode, not both.
func PasscodeRefusal(site model.Site, g GateSite) error {
	if site.NamedViewers {
		return conflict("named_viewers_set", "this site is open only to named viewers; a site has named viewers or a passcode, not both. Set who can open it back to anyone first (set_site_access access anyone, or PUT /v1/sites/{sitename}/access), after the person agrees")
	}
	if !g.OwnAddress {
		return conflict("passcode_needs_own_address", "a passcode needs the site to have an address of its own, and this site is served on an address every site shares")
	}
	return nil
}

// NamedViewersRefusal is why named viewers cannot be turned on for the site,
// nil when they can.
func NamedViewersRefusal(row model.SitePasscodeRow, g GateSite) error {
	switch {
	case g.EventSites:
		return conflict("named_viewers_unavailable", "named viewers are not available on Simple Hack; event and team sites have their own access rules")
	case g.NoVisitorSignIn:
		return conflict("sign_in_unavailable", "named viewers sign in with an emailed code or Google, and this server has neither set up")
	case !g.OwnAddress:
		return conflict("named_viewers_need_own_address", "named viewers need the site to have an address of its own, and this site is served on an address every site shares")
	case row.Enc != nil:
		return conflict("passcode_set", "this site has a passcode; a site has a passcode or named viewers, not both. Remove the passcode first (remove_site_passcode, or DELETE /v1/sites/{sitename}/lock), after the person agrees")
	}
	return nil
}

// ViewerEmails is the list an owner asked to name, normalized and without
// repeats, at most max.
func ViewerEmails(raw []string, max int) ([]string, error) {
	var emails []string
	seen := map[string]bool{}
	for _, r := range raw {
		e, ok := authrules.NormalEmail(r)
		if !ok {
			return nil, &Refusal{http.StatusBadRequest, "invalid_email", "not an email address: " + strings.TrimSpace(r)}
		}
		if !seen[e] {
			seen[e] = true
			emails = append(emails, e)
		}
	}
	if len(emails) > max {
		return nil, &Refusal{http.StatusBadRequest, "too_many_viewers", "a site can name at most " + strconv.Itoa(max) + " viewers"}
	}
	return emails, nil
}

// NewViewers splits emails into those to add and those already named on
// the site, refusing a list that would take it past max.
func (s *Service) NewViewers(ctx context.Context, siteID string, emails []string, max int) (fresh, already []string, err error) {
	have, err := repo.CountSiteViewers(ctx, s.database, siteID)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range emails {
		listed, err := repo.IsSiteViewer(ctx, s.database, siteID, e)
		if err != nil {
			return nil, nil, err
		}
		if listed {
			already = append(already, e)
		} else {
			fresh = append(fresh, e)
		}
	}
	if have+len(fresh) > max {
		return nil, nil, conflict("too_many_viewers", "a site can name at most "+strconv.Itoa(max)+" viewers; it has "+strconv.Itoa(have)+". Remove some first")
	}
	return fresh, already, nil
}
