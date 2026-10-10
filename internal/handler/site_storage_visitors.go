package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/lib/pq"
	"github.com/ncruces/go-sqlite3"
)

// Who sent each record (2026-10-10). A visitor's saves carry an opaque
// visitor_id: the visitor_id column of a read-own SQLite table, and, for the
// owner only, visitor_id on KV keys and stored files. GET
// /v1/sites/{sitename}/storage/visitors?id=<visitor_id>[,<visitor_id>...]
// turns those ids into the email each person signs in with, for the site's
// owner (key or connector) only, and only for ids that saved something on
// this very site: an owner can never look up an account that never wrote to
// their site. Emails are never stored in a site's data and never reach a
// visitor.

// storageVisitorIDsMax is the most ids one lookup takes.
const storageVisitorIDsMax = 100

var storageVisitorIDRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type storageVisitor struct {
	VisitorID string `json:"visitor_id"`
	Email     string `json:"email,omitempty"`
	// Found false: the id saved nothing on this site, or its account is gone.
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
	wrote, err := h.storageWritersAmong(r.Context(), c, ids)
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

// storageWritersAmong reports which of ids saved something on the site: a KV
// key, a stored file, or a row of any SQLite table with a visitor_id column.
func (h *SiteHandler) storageWritersAmong(ctx context.Context, c storageCall, ids []string) (map[string]bool, error) {
	found := map[string]bool{}
	rows, err := h.database.QueryContext(ctx, `
		SELECT writer_id FROM site_storage_kv WHERE site_id = $1 AND writer_id = ANY($2)
		UNION SELECT writer_id FROM site_storage_files WHERE site_id = $1 AND writer_id = ANY($2)`, c.siteID, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		found[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var rest []string
	for _, id := range ids {
		if !found[id] {
			rest = append(rest, id)
		}
	}
	if len(rest) == 0 {
		return found, nil
	}
	names, err := h.database.QueryContext(ctx, `SELECT name FROM site_storage_resources WHERE site_id = $1 AND kind = 'sqlite' ORDER BY name`, c.siteID)
	if err != nil {
		return nil, err
	}
	var resources []string
	for names.Next() {
		var n string
		if err := names.Scan(&n); err != nil {
			names.Close()
			return nil, err
		}
		resources = append(resources, n)
	}
	names.Close()
	if err := names.Err(); err != nil {
		return nil, err
	}
	for _, name := range resources {
		rc := c
		rc.resourceName = name
		if err := h.sqliteWritersAmong(ctx, rc, rest, found); err != nil {
			return nil, err
		}
	}
	return found, nil
}

// sqliteWritersAmong marks the ids found in a visitor_id column of any table
// of one SQLite resource, read-only.
func (h *SiteHandler) sqliteWritersAmong(ctx context.Context, c storageCall, ids []string, found map[string]bool) error {
	path := h.storageSQLPath(c)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	release, err := acquireStorageSQLite(ctx)
	if err != nil {
		return err
	}
	defer release()
	uri := (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}).String()
	conn, err := sqlite3.OpenContext(sqlite3.WithMaxMemory(ctx, storageSQLiteMaxMemory), uri)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetInterrupt(ctx)
	stmt, _, err := conn.Prepare(`SELECT m.name FROM sqlite_schema m JOIN pragma_table_info(m.name) p WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' AND p.name = 'visitor_id'`)
	if err != nil {
		return err
	}
	var tables []string
	for stmt.Step() {
		tables = append(tables, stmt.ColumnText(0))
	}
	if err := stmt.Close(); err != nil {
		return err
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	for _, t := range tables {
		q, _, err := conn.Prepare(`SELECT DISTINCT visitor_id FROM ` + storageQuote(t) + ` WHERE visitor_id IN (` + marks + `)`)
		if err != nil {
			return err
		}
		for i, id := range ids {
			if err := q.BindText(i+1, id); err != nil {
				q.Close()
				return err
			}
		}
		for q.Step() {
			found[q.ColumnText(0)] = true
		}
		if err := q.Close(); err != nil {
			return err
		}
	}
	return nil
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
