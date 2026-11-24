// Package values is how pages, people and their AI apps list, read, set and
// delete a namespace's named values (its _values table), under the
// namespace's rules for _values. Routes 46 to 48 and the key list, at the
// paths published pages call today.
package values

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"simplehost/app"
	"simplehost/core/web"
	data "simplehost/data/core"
	"simplehost/data/core/rules"
)

type handler struct{ a *app.App }

// Mount adds the value routes on the platform and on every site address.
func Mount(a *app.App) {
	h := &handler{a}
	const keys = "/v1/sites/{site}/storage/kv/{ns}/keys"
	for _, mux := range []*http.ServeMux{a.Routes.Platform, a.Routes.Site} {
		mux.Handle("GET "+keys, web.Func(h.list))
		mux.Handle("GET "+keys+"/{key}", web.Func(h.get))
		mux.Handle("PUT "+keys+"/{key}", web.Func(h.put))
		mux.Handle("DELETE "+keys+"/{key}", web.Func(h.remove))
	}
}

const table = "_values"

// item is one value as the caller may see it.
func item(q *data.Req, row []any) map[string]any {
	out := map[string]any{"key": row[0], "value": json.RawMessage(row[1].(string))}
	author, mine := q.Author(row[2])
	if author != nil {
		out["visitor_id"] = author
	}
	if !q.Owner {
		out["mine"] = mine
	}
	return out
}

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
	res, err := q.Query(r.Context(), `SELECT key, value, visitor_id FROM _values WHERE substr(key, 1, length(?)) = ? AND key > ? AND `+cond+
		` ORDER BY key LIMIT ?`, append(append([]any{prefix, prefix, after}, args...), limit+1)...)
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
		items = append(items, item(q, row))
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
	res, err := q.Query(r.Context(), `SELECT key, value, visitor_id FROM _values WHERE key = ? AND `+cond, append([]any{r.PathValue("key")}, args...)...)
	if err != nil {
		return err
	}
	if len(res.Rows) == 0 {
		return web.NotFound
	}
	web.JSON(w, http.StatusOK, item(q, res.Rows[0]))
	return nil
}

// put sets a value: a change where the key exists, else an add, each under
// its rules, and the written row must pass the check.
func (h *handler) put(w http.ResponseWriter, r *http.Request) error {
	q, err := h.a.Data.Begin(r, h.a.Sites, true)
	if err != nil {
		return err
	}
	defer q.Close()
	var in struct{ Value json.RawMessage }
	if json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in) != nil || len(in.Value) == 0 || !json.Valid(in.Value) {
		return web.Refuse(http.StatusBadRequest, "bad_value", `Send the value as JSON: {"value": ...}.`)
	}
	key, value, ctx := r.PathValue("key"), string(in.Value), r.Context()
	if key == "" || len(key) > 512 || strings.ContainsRune(key, 0) {
		return web.Refuse(http.StatusBadRequest, "bad_key", "Use a key of 1 to 512 characters.")
	}
	var author any
	if id := web.CallerOf(r).ID; id != "" {
		author = id
	}
	action := "edit"
	err = q.Tx(ctx, func() error {
		have, err := q.Query(ctx, `SELECT count(*) FROM _values WHERE key = ?`, key)
		if err != nil {
			return err
		}
		op := rules.OpEdit
		if have.Rows[0][0].(int64) == 0 {
			op, action = rules.OpAdd, "add"
			if _, _, err := q.Rule(ctx, table, op, true); err != nil {
				return err
			}
			if _, err := q.Exec(ctx, `INSERT INTO _values (key, value, visitor_id) VALUES (?, ?, ?)`, key, value, author); err != nil {
				return err
			}
		} else {
			cond, args, err := q.Visible(ctx, table, op)
			if err != nil {
				return err
			}
			res, err := q.Exec(ctx, `UPDATE _values SET value = ?, updated_at = CURRENT_TIMESTAMP WHERE key = ? AND `+cond, append([]any{value, key}, args...)...)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return q.Refused(ctx, table, op)
			}
		}
		check, cargs, err := q.Rule(ctx, table, op, true)
		if err != nil {
			return err
		}
		ok, err := q.Query(ctx, `SELECT count(*) FROM _values WHERE key = ? AND `+check, append([]any{key}, cargs...)...)
		if err != nil || ok.Rows[0][0].(int64) > 0 {
			return err
		}
		return q.Refused(ctx, table, op)
	})
	if err == nil {
		err = h.a.Data.Changed(ctx, q.Site, q.Name, table, action)
	}
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]any{"key": key, "value": in.Value})
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
	res, err := q.Exec(r.Context(), `DELETE FROM _values WHERE key = ? AND `+cond, append([]any{r.PathValue("key")}, args...)...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return web.NotFound
	}
	if err := h.a.Data.Changed(r.Context(), q.Site, q.Name, table, "delete"); err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]any{"deleted": true})
	return nil
}
