package visitors

import (
	"net/http"

	"github.com/vsriram/simple-host/host/data"

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

func storageVisitors(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call) {
	visitors, err := Visitors(h.StorageSvc(), r.Context(), VisitorsInput{Call: c, IDs: r.URL.Query()["id"], Emails: r.URL.Query()["email"]})
	if err != nil {
		data.WriteStorageError(w, err, 0)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"visitors": visitors})
}

// storagePeople serves GET /v1/sites/{sitename}/storage/people: the site's
// people with saved data or named-viewer status, for the owner to pick
// whom a new row is for (Person). Owner credential, or the owner
// signed in on the site's own address (SH.me().site_owner), only.
func storagePeople(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call) {
	people, err := SitePeople(h.StorageFor(c), r.Context(), c)
	if err != nil {
		data.WriteStorageError(w, err, 0)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"people": people})
}

// Register maps the visitor lookups behind caller (Access's StorageCaller).
func Register(mux *http.ServeMux, h *data.Handler, caller func(http.Handler) http.Handler) {
	mux.Handle("GET /v1/sites/{sitename}/storage/visitors", h.StorageRouteFor(caller, storageVisitors))
	mux.Handle("GET /v1/sites/{sitename}/storage/people", h.StorageRouteFor(caller, storagePeople))
}
