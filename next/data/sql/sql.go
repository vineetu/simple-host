// Package sql is the owner's SQL on their own site's namespaces: any
// statement, on any table, values and file records included. Only the two
// guards apply: the site's data size cap and the time limit per query.
// Everyone else uses the row, value and file routes.
package sql

import (
	"encoding/json"
	"io"
	"net/http"

	"simplehost/app"
	"simplehost/core/limits"
	"simplehost/core/web"
	data "simplehost/data/core"
)

type handler struct{ a *app.App }

// Mount adds the owner SQL routes, at today's paths, on the platform (the
// owner's AI app and scripts) and on every site address (the owner's page).
func Mount(a *app.App) {
	h := &handler{a}
	const base = "/v1/sites/{site}/storage/sqlite/{ns}/"
	for _, mux := range []*http.ServeMux{a.Routes.Platform, a.Routes.Site} {
		mux.Handle("POST "+base+"query", web.Func(h.query))
		mux.Handle("POST "+base+"execute", web.Func(h.execute))
		mux.Handle("POST "+base+"schema", web.Func(h.schema))
	}
}

type input struct {
	SQL    string `json:"sql"`
	Params []any  `json:"params"`
}

// open checks the caller is the site's owner, reads the statement, and
// opens the namespace, making it first when create is set.
func (h *handler) open(r *http.Request, create, needSQL bool) (*data.NS, input, error) {
	var in input
	site, err := h.a.Sites.Of(r)
	if err == nil {
		err = h.a.Sites.Manage(r, site)
	}
	if err != nil {
		return nil, in, err
	}
	d := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	d.UseNumber()
	if err := d.Decode(&in); (err != nil && err != io.EOF) || needSQL && in.SQL == "" {
		return nil, in, web.Refuse(http.StatusBadRequest, "bad_json", `Send {"sql": "...", "params": [...]}.`)
	}
	for i, p := range in.Params {
		if n, ok := p.(json.Number); ok {
			if in.Params[i], err = n.Int64(); err != nil {
				in.Params[i], _ = n.Float64()
			}
		}
	}
	if create && in.SQL != "" { // the table list alone makes nothing
		if err := h.a.Data.Create(r.Context(), site, r.PathValue("ns")); err != nil {
			return nil, in, err
		}
	}
	ns, err := h.a.Data.Open(r, site, r.PathValue("ns"))
	return ns, in, err
}

func (h *handler) query(w http.ResponseWriter, r *http.Request) error {
	ns, in, err := h.open(r, false, true)
	if err != nil {
		return err
	}
	defer ns.Close()
	if !ns.ReadsOnly(in.SQL) {
		return web.Refuse(http.StatusBadRequest, "not_a_read", "query runs one statement that only reads, like SELECT. To change data, use execute.")
	}
	res, err := ns.Query(r.Context(), in.SQL, in.Params...)
	if err != nil {
		return err
	}
	if res.Rows == nil {
		res.Rows = [][]any{}
	}
	web.JSON(w, http.StatusOK, res)
	return nil
}

func (h *handler) execute(w http.ResponseWriter, r *http.Request) error {
	ns, in, err := h.open(r, true, true)
	if err != nil {
		return err
	}
	defer ns.Close()
	res, err := ns.Exec(r.Context(), in.SQL, in.Params...)
	if err != nil {
		return err
	}
	changes, _ := res.RowsAffected()
	id, _ := res.LastInsertId()
	web.JSON(w, http.StatusOK, map[string]any{"changes": changes, "last_insert_id": id})
	return nil
}

// schema runs the statement when one is sent (CREATE, ALTER, DROP; the
// namespace is made first if new), then answers with every table and the
// space the namespace and the site use.
func (h *handler) schema(w http.ResponseWriter, r *http.Request) error {
	ns, in, err := h.open(r, true, false)
	if err != nil {
		return err
	}
	defer ns.Close()
	var changes int64
	if in.SQL != "" {
		res, err := ns.Exec(r.Context(), in.SQL, in.Params...)
		if err != nil {
			return err
		}
		changes, _ = res.RowsAffected()
	}
	tables, err := ns.Query(r.Context(), `SELECT name, sql FROM sqlite_schema WHERE type = 'table' ORDER BY name`)
	if err != nil {
		return err
	}
	list := []map[string]any{}
	for _, t := range tables.Rows {
		list = append(list, map[string]any{"name": t[0], "sql": t[1]})
	}
	sizes, total := h.a.Data.Sizes(ns.Site.ID)
	web.JSON(w, http.StatusOK, map[string]any{"changes": changes, "tables": list,
		"bytes": sizes[ns.Name], "site_bytes": total, "site_limit": limits.DataBytes.N})
	return nil
}
