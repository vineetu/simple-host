// Package files is how pages, people and their AI apps list, fetch, upload
// and delete files in a namespace's file store, under the namespace's rules
// for _files. Uploads go through the one store data/core keeps, the same one
// the owner's upload links fill. Routes 49 to 51 and the list, at the paths
// published pages call today.
package files

import (
	"net/http"
	"os"

	"simplehost/app"
	"simplehost/core/web"
	data "simplehost/data/core"
	"simplehost/data/core/rules"
)

type handler struct{ a *app.App }

// Mount adds the file routes on the platform and on every site address.
func Mount(a *app.App) {
	h := &handler{a}
	const objects = "/v1/sites/{site}/storage/files/{ns}/objects"
	for _, mux := range []*http.ServeMux{a.Routes.Platform, a.Routes.Site} {
		mux.Handle("GET "+objects, web.Func(h.list))
		mux.Handle("GET "+objects+"/{path...}", web.Func(h.get))
		mux.Handle("PUT "+objects+"/{path...}", web.Func(h.put))
		mux.Handle("DELETE "+objects+"/{path...}", web.Func(h.remove))
	}
}

const table = "_files"

func (h *handler) list(w http.ResponseWriter, r *http.Request) error {
	q, err := h.a.Data.Begin(r, h.a.Sites, false)
	if err != nil {
		return err
	}
	defer q.Close()
	cond, args, err := q.Rule(r.Context(), table, rules.OpRead, false)
	if err != nil {
		return err
	}
	prefix, after, limit := r.URL.Query().Get("prefix"), r.URL.Query().Get("after"), data.Limit(r)
	res, err := q.Query(r.Context(), `SELECT name, size, type, visitor_id FROM _files WHERE substr(name, 1, length(?)) = ? AND name > ? AND `+cond+
		` ORDER BY name LIMIT ?`, append(append([]any{prefix, prefix, after}, args...), limit+1)...)
	if err != nil {
		return err
	}
	next := ""
	if len(res.Rows) > limit {
		res.Rows = res.Rows[:limit]
		next = res.Rows[limit-1][0].(string)
	}
	items := make([]map[string]any, 0, len(res.Rows))
	for _, row := range res.Rows {
		it := map[string]any{"path": row[0], "bytes": row[1], "content_type": row[2]}
		author, mine := q.Author(row[3])
		if author != nil {
			it["visitor_id"] = author
		}
		if !q.Owner {
			it["mine"] = mine
		}
		items = append(items, it)
	}
	web.JSON(w, http.StatusOK, map[string]any{"items": items, "next_after": next})
	return nil
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) error {
	q, err := h.a.Data.Begin(r, h.a.Sites, false)
	if err != nil {
		return err
	}
	defer q.Close()
	cond, args, err := q.Rule(r.Context(), table, rules.OpRead, false)
	if err != nil {
		return err
	}
	name := r.PathValue("path")
	file, err := h.a.Data.FilePath(q.Site.ID, q.Name, name)
	if err != nil {
		return web.NotFound
	}
	res, err := q.Query(r.Context(), `SELECT type FROM _files WHERE name = ? AND `+cond, append([]any{name}, args...)...)
	if err != nil {
		return err
	}
	if len(res.Rows) == 0 {
		return web.NotFound
	}
	f, err := os.Open(file)
	if err != nil {
		return web.NotFound
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if t, _ := res.Rows[0][0].(string); t != "" {
		w.Header().Set("Content-Type", t)
	}
	// A stored file never runs as a page of the site: no scripts, no sniffing.
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", info.ModTime(), f)
	return nil
}

// put stores the body under the path. Someone who is not the owner must be
// let to add (or, for an existing file, to change it), and the file as it
// would be recorded must pass the check, before any byte is kept.
func (h *handler) put(w http.ResponseWriter, r *http.Request) error {
	q, err := h.a.Data.Begin(r, h.a.Sites, true)
	if err != nil {
		return err
	}
	defer q.Close()
	ctx, name, c := r.Context(), r.PathValue("path"), web.CallerOf(r)
	if err := data.ValidFileName(name); err != nil {
		return err
	}
	ctype := r.Header.Get("Content-Type")
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	have, err := q.Query(ctx, `SELECT visitor_id, created_at FROM _files WHERE name = ?`, name)
	if err != nil {
		return err
	}
	op, action, author, created := rules.OpAdd, "add", any(nil), any(nil)
	if c.ID != "" {
		author = c.ID
	}
	if len(have.Rows) > 0 {
		op, action, author, created = rules.OpEdit, "edit", have.Rows[0][0], have.Rows[0][1]
		cond, args, err := q.Visible(ctx, table, op)
		if err != nil {
			return err
		}
		ok, err := q.Query(ctx, `SELECT count(*) FROM _files WHERE name = ? AND `+cond, append([]any{name}, args...)...)
		if err != nil {
			return err
		}
		if ok.Rows[0][0].(int64) == 0 {
			return q.Refused(ctx, table, op)
		}
	}
	check, cargs, err := q.Rule(ctx, table, op, true)
	if err != nil {
		return err
	}
	ok, err := q.Query(ctx, `SELECT count(*) FROM (SELECT ? AS name, ? AS size, ? AS type, ? AS visitor_id, COALESCE(?, CURRENT_TIMESTAMP) AS created_at) WHERE `+check,
		append([]any{name, r.ContentLength, ctype, author, created}, cargs...)...)
	if err != nil {
		return err
	}
	if ok.Rows[0][0].(int64) == 0 {
		return q.Refused(ctx, table, op)
	}
	err = h.a.Data.PutFile(r, q.NS, name, ctype, c.ID, r.Body)
	if err == nil {
		err = h.a.Data.Changed(ctx, q.Site, q.Name, table, action)
	}
	var stored data.Rows
	if err == nil {
		stored, err = q.Query(ctx, `SELECT size FROM _files WHERE name = ?`, name)
	}
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]any{"path": name, "bytes": stored.Rows[0][0], "content_type": ctype})
	return nil
}

func (h *handler) remove(w http.ResponseWriter, r *http.Request) error {
	q, err := h.a.Data.Begin(r, h.a.Sites, true)
	if err != nil {
		return err
	}
	defer q.Close()
	cond, args, err := q.Visible(r.Context(), table, rules.OpDelete)
	if err != nil {
		return err
	}
	name := r.PathValue("path")
	file, err := h.a.Data.FilePath(q.Site.ID, q.Name, name)
	if err != nil {
		return web.NotFound
	}
	res, err := q.Exec(r.Context(), `DELETE FROM _files WHERE name = ? AND `+cond, append([]any{name}, args...)...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if q.Owner {
			// The owner's delete of a file that is not there is done already.
			web.JSON(w, http.StatusOK, map[string]any{"deleted": true})
			return nil
		}
		return web.NotFound
	}
	os.Remove(file)
	if err := h.a.Data.Changed(r.Context(), q.Site, q.Name, table, "delete"); err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]any{"deleted": true})
	return nil
}
