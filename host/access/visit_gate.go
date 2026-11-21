package access

import (
	"database/sql"
	"errors"

	"github.com/vsriram/simple-host/host/model"
)

// VisitKind is what a request wants from a site the visitor gate guards. The
// kinds differ only in who is eligible, never in the order of the rules.
type VisitKind int

const (
	// VisitPage: a file of the site, opened in a browser. Only the visitor's
	// own unlock and sign-in count; a key, a preview page or a shared host
	// never open a page.
	VisitPage VisitKind = iota
	// VisitData: the site's saved-data API. The key of someone who acts as
	// the owner, or of the admin, passes both rules, and a read from a
	// preview page of the site passes both.
	VisitData
	// VisitSignIn: the visitor sign-in routes. The passcode applies as for
	// VisitData; named viewers do not, since signing in is how a named
	// viewer gets in.
	VisitSignIn
)

// VisitVerdict is what the visitor gate does with a request.
type VisitVerdict int

const (
	// VisitServe: no gate applies, or the visitor passes it.
	VisitServe VisitVerdict = iota
	// VisitTakedown: the site is taken down (page: 410).
	VisitTakedown
	// VisitOffline: the owner took the site offline (page: 503).
	VisitOffline
	// VisitNotFound: the directory being served is no longer this site's.
	VisitNotFound
	// VisitServiceError: the marker says protected and the database cannot
	// say otherwise, or the viewers list cannot be read; fail closed.
	VisitServiceError
	// VisitElsewhere: a page of a protected site on a host every site shares,
	// where no passcode or sign-in is ever valid: send the visitor to the
	// site's own address.
	VisitElsewhere
	// VisitNeedPasscode: the passcode refuses (page: the passcode form; API:
	// 403 site_locked).
	VisitNeedPasscode
	// VisitNeedSignIn: named viewers, and no session counts (page: the
	// sign-in page; API: 403 site_private).
	VisitNeedSignIn
	// VisitNotListed: named viewers, and the signed-in person is not on the
	// list (page: the private page; API: 403 site_private).
	VisitNotListed
	// VisitFramedOut: a page of a named-viewers site asked for by another
	// site's page (a subresource or a frame): never shown (403).
	VisitFramedOut
)

// Visit is what the gate knows about one request: the site folder's markers
// (pages only; the API reads the site's state itself), the site's protection
// row, whether the request host is shared by every site (pages only), and
// what the visitor carries.
type Visit struct {
	Kind                          VisitKind
	Suspended, Offline, Protected bool
	Row                           model.SitePasscodeRow
	// RowErr is the error loading Row; sql.ErrNoRows means no such live site.
	RowErr error
	// Bound: Row was loaded against the opened directory, so a missing row
	// means the directory is no longer this site's.
	Bound      bool
	SharedHost bool
	Who        Visitor
}

// Visitor is what a request carries toward a protected site, as the edge
// measured it. Facts a kind does not use are ignored.
type Visitor struct {
	// Read: GET or HEAD.
	Read bool
	// OwnerKey: the key or connector token of someone who acts as the
	// site's owner (ActsAsOwner). AdminKey: the admin's.
	OwnerKey, AdminKey bool
	// Unlocked: this browser holds a valid unlock for the site on this host.
	Unlocked bool
	// CrossSite: another site's page asked for this (a subresource, not a
	// navigation). Framed: another site's page frames it.
	CrossSite, Framed bool
	// SameOrigin: the request came from a page on this very host (or is an
	// AI visitor's internal request).
	SameOrigin bool
	// Preview: the request comes from a preview page of this site whose
	// token holds.
	Preview bool
	// Session: a visitor is signed in to the site on this host. Listed: that
	// person may open it (ViewerAllowed); ListErr: the list could not be read.
	Session, Listed bool
	ListErr         error
}

// DecideVisit is the visitor gate, the one decision every page, saved-data
// route and visitor tool asks: take-down, then offline, then the protection
// the database holds now, then named viewers, then the passcode.
func DecideVisit(v Visit) VisitVerdict {
	switch {
	case v.Suspended:
		return VisitTakedown
	case v.Offline:
		return VisitOffline
	case !v.Protected:
		return VisitServe
	case errors.Is(v.RowErr, sql.ErrNoRows) && v.Bound:
		return VisitNotFound
	case errors.Is(v.RowErr, sql.ErrNoRows):
		return VisitServe // no such live site: the caller's 404 stands
	case v.RowErr != nil:
		return VisitServiceError
	case v.Row.Enc == nil && !v.Row.NamedViewers:
		return VisitServe // unlocked in the database; the marker is on its way out
	case v.SharedHost && v.Kind == VisitPage:
		return VisitElsewhere
	}
	if v.Kind != VisitSignIn {
		if verdict := ViewersAdmit(v.Kind, v.Row, v.Who); verdict != VisitServe {
			return verdict
		}
	}
	if !PasscodeAdmits(v.Kind, v.Row, v.Who) {
		return VisitNeedPasscode
	}
	return VisitServe
}

// PasscodeAdmits is the passcode rule: a site without one lets everyone in;
// one with a passcode lets in a browser holding a valid unlock for it on this
// host, except for a file another site's page asked for (an unlock is never
// lent to a cross-site subresource). The API also lets in the owner's or the
// admin's key and a read from a preview page of the site.
func PasscodeAdmits(kind VisitKind, row model.SitePasscodeRow, who Visitor) bool {
	switch {
	case row.Enc == nil:
		return true
	case who.Unlocked && !who.CrossSite:
		return true
	case kind == VisitPage:
		return false
	}
	return who.OwnerKey || who.AdminKey || who.Read && who.Preview
}

// ViewersAdmit is the named-viewers rule: the owner or a named viewer,
// signed in to the site on this host. A page is never shown inside, or
// fetched by, another site's page. The API also lets in the key of someone
// who acts as the owner or of the admin, and a read from a preview page, and counts a session
// only for a same-origin request or a reading navigation (a link to a stored
// file).
func ViewersAdmit(kind VisitKind, row model.SitePasscodeRow, who Visitor) VisitVerdict {
	if !row.NamedViewers || kind == VisitSignIn {
		return VisitServe
	}
	session := who.Session
	if kind == VisitPage {
		if who.CrossSite || who.Framed {
			return VisitFramedOut
		}
	} else {
		if who.OwnerKey || who.AdminKey || who.Read && who.Preview {
			return VisitServe
		}
		session = session && (who.SameOrigin || who.Read && !who.CrossSite && !who.Framed)
	}
	switch {
	case !session:
		return VisitNeedSignIn
	case who.ListErr != nil:
		return VisitServiceError
	case !who.Listed:
		return VisitNotListed
	}
	return VisitServe
}

// NoVisitorSignIn: the install has no way for a visitor to sign in (no
// emailed codes and no sign-in provider).
func NoVisitorSignIn(email bool, providers []string) bool { return !email && len(providers) == 0 }
