package access

import (
	"database/sql"
	"errors"

	"github.com/vsriram/simple-host/host/model"
)

// VisitVerdict is what the visitor gate does with a request for a site's file.
type VisitVerdict int

const (
	// VisitServe: no gate applies; the file is served.
	VisitServe VisitVerdict = iota
	// VisitTakedown: the site is taken down (410).
	VisitTakedown
	// VisitOffline: the owner took the site offline (503).
	VisitOffline
	// VisitNotFound: the directory being served is no longer this site's.
	VisitNotFound
	// VisitServiceError: the marker says protected and the database cannot
	// say otherwise; fail closed.
	VisitServiceError
	// VisitElsewhere: protected, on a host every site shares, where no
	// passcode or sign-in is ever valid: send the visitor to the site's own
	// address.
	VisitElsewhere
	// VisitCheckVisitor: protected on the site's own address: the named
	// viewers list and the passcode decide (VisitorLetIn).
	VisitCheckVisitor
)

// Visit is what the gate knows about one request before it looks at the
// visitor: the site folder's markers, the site's protection row (loaded only
// when Protected), and whether the request host is shared by every site.
type Visit struct {
	Suspended, Offline, Protected bool
	Row                           model.SitePasscodeRow
	// RowErr is the error loading Row; sql.ErrNoRows means no such live site.
	RowErr error
	// Bound: Row was loaded against the opened directory, so a missing row
	// means the directory is no longer this site's.
	Bound      bool
	SharedHost bool
}

// DecideVisit is the order of the visitor gate: take-down, then offline, then
// the protection the database holds now.
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
	case v.SharedHost:
		return VisitElsewhere
	}
	return VisitCheckVisitor
}

// PasscodeLetsVisitorIn: a site with a passcode lets in a browser holding a
// valid unlock for it on this host, except for a request another site's page
// made for a file (an unlock is never lent to a cross-site subresource).
func PasscodeLetsVisitorIn(row model.SitePasscodeRow, unlocked, crossSiteSubresource bool) bool {
	return row.Enc == nil || unlocked && !crossSiteSubresource
}

// NoVisitorSignIn: the install has no way for a visitor to sign in (no
// emailed codes and no sign-in provider).
func NoVisitorSignIn(email bool, providers []string) bool { return !email && len(providers) == 0 }
