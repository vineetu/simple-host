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
	if len(ids) == 0 || len(ids) > storageVisitorIDsMax {
		storageError(w, 400, "invalid_visitor_id", "give 1 to 100 visitor ids: ?id=<visitor_id>,<visitor_id>")
		return
	}
	visitors, err := h.storageSvc().Visitors(r.Context(), appstorage.VisitorsInput{SiteID: c.siteID, IDs: ids})
	if err != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	out := make([]storageVisitor, 0, len(visitors))
	for _, v := range visitors {
		out = append(out, storageVisitor{VisitorID: v.VisitorID, Email: v.Email, Found: v.Found})
	}
	writeJSON(w, 200, map[string]any{"visitors": out})
}
