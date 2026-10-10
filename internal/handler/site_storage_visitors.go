package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/lib/pq"
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
	if _, event := r.Context().Value(eventStorageContextKey{}).(eventStorageScope); event {
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
	wrote, err := h.storageSignedInAmong(r.Context(), c, ids)
	if err != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	out := make([]storageVisitor, 0, len(ids))
	for _, id := range ids {
		v := storageVisitor{VisitorID: id}
		if wrote[id] {
			email, err := visitorEmail(r.Context(), h.database, id)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				storageError(w, 500, "internal_error", "internal server error")
				return
			}
			v.Email, v.Found = email, err == nil && email != ""
		}
		out = append(out, v)
	}
	writeJSON(w, 200, map[string]any{"visitors": out})
}

// storageSignedInAmong reports which of ids signed in on the site as a
// visitor (site_visitor_signins, which the server writes whenever a visitor
// session starts; db.InsertVisitorSession). Every visitor_id the server
// stamps on a save belongs to such a person. It is never read from the site's
// own data, which the owner can write. A page on the site can already read a
// signed-in visitor's email (SH.me()), so the owner learns nothing new.
func (h *SiteHandler) storageSignedInAmong(ctx context.Context, c storageCall, ids []string) (map[string]bool, error) {
	rows, err := h.database.QueryContext(ctx, `SELECT user_id::text FROM site_visitor_signins WHERE site_id = $1 AND user_id::text = ANY($2)`, c.siteID, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		found[id] = true
	}
	return found, rows.Err()
}

// storageFileWriters is who uploaded each of paths in one files resource
// (empty: the owner).
func (h *SiteHandler) storageFileWriters(ctx context.Context, c storageCall, paths []string) (map[string]string, error) {
	rows, err := h.database.QueryContext(ctx, `SELECT path, writer_id FROM site_storage_files WHERE site_id = $1 AND resource_name = $2 AND path = ANY($3)`, c.siteID, c.resourceName, pq.Array(paths))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, wid string
		if err := rows.Scan(&p, &wid); err != nil {
			return nil, err
		}
		out[p] = wid
	}
	return out, rows.Err()
}
