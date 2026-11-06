package handler

import (
	"net/http"
	"regexp"
	"strings"

	appstorage "github.com/vsriram/simple-host/internal/app/storage"
)

// Who sent each record (2026-10-10). A visitor's saves carry an opaque
// visitor_id: the visitor_id column of a read-own SQLite table, and, for the
// owner only, visitor_id on KV keys and stored files. GET
// /v1/sites/{sitename}/storage/visitors?id=<visitor_id>[,<visitor_id>...]
// turns those ids into the email each person signs in with, for the site's
// owner (key or connector) only, and only for people who signed in on this
// very site as visitors (recorded by the server, never taken from the site's
// own tables): an owner can never look up an account that has nothing to do
// with their site. Emails are never stored in a site's data and never reach a
// visitor.
//
// ?email=<address>[,<address>...] resolves the other direction (2026-11-06,
// SIM-358): the visitor_id each address signed in as on this site, for
// building a POST .../tables/{t}/rows?for=<visitor_id or email> request, or
// for ResolveOwnerFor directly with the address itself. id and email may be
// combined in one call; each is resolved independently and the results
// concatenated in that order. The same "only people who signed in here"
// rule applies; there is no pre-sign-in email binding.

// storageVisitorIDsMax is the most ids one lookup takes.
const storageVisitorIDsMax = 100

var storageVisitorIDRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type storageVisitor struct {
	VisitorID string `json:"visitor_id"`
	Email     string `json:"email,omitempty"`
	// Found false: the id never signed in on this site, or its account is gone.
	Found bool `json:"found"`
}

func (h *SiteHandler) storageVisitors(w http.ResponseWriter, r *http.Request) {
	if _, event := r.Context().Value(eventStorageContextKey{}).(eventStorageScope); event || h.ed.Storage().EventOwners {
		storageError(w, 404, "not_found", "storage route not found")
		return
	}
	c, ok := h.storageSite(w, r)
	if !ok || !h.storageOwner(w, c) {
		return
	}
	var ids []string
	seen := map[string]bool{}
	for _, v := range r.URL.Query()["id"] {
		for _, id := range strings.Split(v, ",") {
			id = strings.ToLower(strings.TrimSpace(id))
			if id == "" || seen[id] {
				continue
			}
			if !storageVisitorIDRE.MatchString(id) {
				storageError(w, 400, "invalid_visitor_id", "visitor ids look like 1b4e28ba-2fa1-11d2-883f-0016d3cca427, as in a row's visitor_id")
				return
			}
			seen[id] = true
			ids = append(ids, id)
		}
	}
	var emails []string
	seenEmail := map[string]bool{}
	for _, v := range r.URL.Query()["email"] {
		for _, email := range strings.Split(v, ",") {
			email = strings.ToLower(strings.TrimSpace(email))
			if email == "" || seenEmail[email] {
				continue
			}
			if !strings.Contains(email, "@") {
				storageError(w, 400, "invalid_visitor_id", "email looks like name@example.com")
				return
			}
			seenEmail[email] = true
			emails = append(emails, email)
		}
	}
	total := len(ids) + len(emails)
	if total == 0 || total > storageVisitorIDsMax {
		storageError(w, 400, "invalid_visitor_id", "give 1 to 100 visitor ids or emails: ?id=<visitor_id>,<visitor_id> and/or ?email=<address>,<address>")
		return
	}
	visitors, err := h.storageSvc().Visitors(r.Context(), appstorage.VisitorsInput{Call: h.storageServiceCall(c), IDs: ids, Emails: emails})
	if err != nil {
		if e, ok := err.(*appstorage.Error); ok && e.Code == "forbidden" {
			storageError(w, 403, e.Code, e.Message)
			return
		}
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	out := make([]storageVisitor, 0, len(visitors))
	for _, v := range visitors {
		out = append(out, storageVisitor{VisitorID: v.VisitorID, Email: v.Email, Found: v.Found})
	}
	writeJSON(w, 200, map[string]any{"visitors": out})
}

// storagePerson is one entry of the owner's pick list (GET
// .../storage/people): who they may add a row for, with why they are
// listed. It never includes a signed-in visitor who merely signed in, with
// no saved data and no named-viewer status (the privacy rule).
type storagePerson struct {
	VisitorID   string `json:"visitor_id,omitempty"`
	Email       string `json:"email,omitempty"`
	NamedViewer bool   `json:"named_viewer"`
	HasData     bool   `json:"has_data"`
	SignedIn    bool   `json:"signed_in"`
}

// storagePeople serves GET /v1/sites/{sitename}/storage/people: the site's
// people with saved data or named-viewer status, for the owner to pick
// whom a new row is for (SIM-358). Owner credential, or the owner signed in
// on the site's own address (SH.me().site_owner), only.
func (h *SiteHandler) storagePeople(w http.ResponseWriter, r *http.Request) {
	// Named viewers (one input to this list) are not a Simple Hack concept,
	// and storage_visitor_emails (email resolution, the other input) is
	// already excluded there; keep this list Host-only too, the same as
	// storageVisitors above.
	if _, event := r.Context().Value(eventStorageContextKey{}).(eventStorageScope); event || h.ed.Storage().EventOwners {
		storageError(w, 404, "not_found", "storage route not found")
		return
	}
	c, ok := h.storageSite(w, r)
	if !ok {
		return
	}
	// storageSite resolves an API key or connector token (c.owner) but, for
	// this site-level route (no resource), never a visitor session: do that
	// here, exactly as storageResourceFor does per resource, to let the
	// owner signed in on the site's own address reach this too.
	if !c.owner {
		if sess, ok := h.strictVisitorSession(r, c.siteID); ok && sess.UserID != "" {
			c.visitorSession = sess
			c.visitorID = sess.UserID
			c.ownerOnSite = h.storageSiteOwnerOnSite(r, c)
		}
	}
	call := h.storageServiceCall(c)
	if !call.Owner && !call.OwnerOnSite {
		storageError(w, 403, "forbidden", "owner credential required")
		return
	}
	// SitePeople opens every SQLite resource's own database directly, so it
	// needs the request-scoped deps (SQLiteGate and friends), not the base
	// storageSvc() (unset, since most storageSvc() callers never touch
	// SQLite).
	people, err := h.storageRequestSvc(r, c).SitePeople(r.Context(), call)
	if err != nil {
		if e, ok := err.(*appstorage.Error); ok && e.Code == "forbidden" {
			storageError(w, 403, e.Code, e.Message)
			return
		}
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	out := make([]storagePerson, 0, len(people))
	for _, p := range people {
		out = append(out, storagePerson{VisitorID: p.VisitorID, Email: p.Email, NamedViewer: p.NamedViewer, HasData: p.HasData, SignedIn: p.SignedIn})
	}
	writeJSON(w, 200, map[string]any{"people": out})
}
