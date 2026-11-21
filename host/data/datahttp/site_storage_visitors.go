package datahttp

import (
	"net/http"

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/httpx"
)

// Who sent each record. A visitor's saves carry an opaque
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
// ?email=<address>[,<address>...] resolves the other direction: the visitor_id each address signed in as on this site, for
// building a POST .../tables/{t}/rows?for=<visitor_id or email> request, or
// for ResolveOwnerFor directly with the address itself. id and email may be
// combined in one call; each is resolved independently and the results
// concatenated in that order. The same "only people who signed in here"
// rule applies; there is no pre-sign-in email binding.

type storageVisitor struct {
	VisitorID string `json:"visitor_id"`
	Email     string `json:"email,omitempty"`
	// Found false: the id never signed in on this site, or its account is gone.
	Found bool `json:"found"`
}

func (h *DataHandler) storageVisitors(w http.ResponseWriter, r *http.Request, c StorageCall) {
	if h.storageBindings != nil {
		httpx.WriteError(w, 404, "not_found", "storage route not found")
		return
	}
	if !storageOwner(w, c) {
		return
	}
	ids, emails, err := appstorage.VisitorRefs(r.URL.Query()["id"], r.URL.Query()["email"])
	if err != nil {
		WriteStorageError(w, err, 0)
		return
	}
	visitors, err := h.StorageSvc().Visitors(r.Context(), appstorage.VisitorsInput{Call: c, IDs: ids, Emails: emails})
	if err != nil {
		WriteStorageError(w, err, 0)
		return
	}
	out := make([]storageVisitor, 0, len(visitors))
	for _, v := range visitors {
		out = append(out, storageVisitor{VisitorID: v.VisitorID, Email: v.Email, Found: v.Found})
	}
	httpx.WriteJSON(w, 200, map[string]any{"visitors": out})
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
// whom a new row is for. Owner credential, or the owner signed in
// on the site's own address (SH.me().site_owner), only.
func (h *DataHandler) storagePeople(w http.ResponseWriter, r *http.Request, c StorageCall) {
	// Named viewers (one input to this list) are not a Simple Hack concept,
	// and storage_visitor_emails (email resolution, the other input) is
	// already excluded there; keep this list Host-only too, the same as
	// storageVisitors above.
	if h.storageBindings != nil {
		httpx.WriteError(w, 404, "not_found", "storage route not found")
		return
	}
	svc := h.StorageFor(c)
	c.OwnerOnSite = svc.OwnerOnSite(r.Context(), c)
	if !c.Owner && !c.OwnerOnSite {
		httpx.WriteError(w, 403, "forbidden", "owner credential required")
		return
	}
	people, err := svc.SitePeople(r.Context(), c)
	if err != nil {
		WriteStorageError(w, err, 0)
		return
	}
	out := make([]storagePerson, 0, len(people))
	for _, p := range people {
		out = append(out, storagePerson{VisitorID: p.VisitorID, Email: p.Email, NamedViewer: p.NamedViewer, HasData: p.HasData, SignedIn: p.SignedIn})
	}
	httpx.WriteJSON(w, 200, map[string]any{"people": out})
}
