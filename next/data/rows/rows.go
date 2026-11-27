// Package rows is how pages, people and their AI apps list, add, read,
// change and delete rows of a site's tables, under the namespace's rules.
// The owner passes every rule. Routes 41 to 45, at the paths published
// pages call today.
package rows

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"simplehost/app"
	"simplehost/core/web"
	data "simplehost/data/core"
	"simplehost/data/core/rules"
)

type handler struct{ a *app.App }

// Mount adds the row routes on the platform and on every site address.
func Mount(a *app.App) {
	h := &handler{a}
	const rows = "/v1/sites/{site}/storage/sqlite/{ns}/tables/{table}/rows"
	for _, mux := range []*http.ServeMux{a.Routes.Platform, a.Routes.Site} {
		mux.Handle("GET "+rows, web.Func(h.list))
		mux.Handle("POST "+rows, web.Func(h.add))
		mux.Handle("GET "+rows+"/{id}", web.Func(h.get))
		mux.Handle("PATCH "+rows+"/{id}", web.Func(h.edit))
		mux.Handle("DELETE "+rows+"/{id}", web.Func(h.remove))
	}
}

var ident = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,62}$`)

func quote(name string) string { return `"` + name + `"` }

// table is one open table of a request: its namespace and its columns.
type table struct {
	*data.Req
	name string
	cols map[string]bool
}

func (h *handler) open(r *http.Request, write bool) (*table, error) {
	q, err := h.a.Data.Begin(r, h.a.Sites, write)
	if err != nil {
		return nil, err
	}
	t := &table{Req: q, name: r.PathValue("table"), cols: map[string]bool{}}
	if ident.MatchString(t.name) {
		res, err := q.Query(r.Context(), `SELECT name FROM pragma_table_info(?)`, t.name)
		if err != nil {
			q.Close()
			return nil, err
		}
		for _, c := range res.Rows {
			t.cols[c[0].(string)] = true
		}
	}
	if len(t.cols) == 0 {
		q.Close()
		return nil, web.NotFound
	}
	return t, nil
}

// shape drops the trailing _rowid column and hides other people's
// visitor_id from a caller who is not the owner.
func (t *table) shape(res data.Rows) ([]string, [][]any, []bool) {
	cols := res.Columns[:len(res.Columns)-1]
	author := -1
	for i, c := range cols {
		if c == "visitor_id" {
			author = i
		}
	}
	out, mine := make([][]any, 0, len(res.Rows)), make([]bool, 0, len(res.Rows))
	for _, row := range res.Rows {
		row, m := row[:len(cols)], false
		if author >= 0 {
			row[author], m = t.Author(row[author])
		}
		out, mine = append(out, row), append(mine, m)
	}
	return cols, out, mine
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) error {
	t, err := h.open(r, false)
	if err != nil {
		return err
	}
	defer t.Close()
	cond, args, err := t.Rule(r.Context(), t.name, rules.OpRead, false)
	if err != nil {
		return err
	}
	q, where := r.URL.Query(), []string{cond}
	order := q.Get("order")
	if order == "" {
		order = "_rowid"
	} else if !t.cols[order] || order == "visitor_id" && !t.Owner {
		return web.Refuse(http.StatusBadRequest, "bad_order", "Order by one of the table's columns.")
	}
	for key, vals := range q {
		if col, ok := strings.CutPrefix(key, "where."); ok {
			if !t.cols[col] {
				return web.Refuse(http.StatusBadRequest, "bad_column", "Filter on one of the table's columns, not "+col+".")
			}
			if col == "visitor_id" && !t.Owner && vals[0] != web.CallerOf(r).ID {
				// Authors are hidden from others: no ordering or matching on them.
				return web.Refuse(http.StatusBadRequest, "bad_column", "Filter visitor_id only on your own id.")
			}
			where, args = append(where, quote(col)+" = ?"), append(args, vals[0])
		}
	}
	ord, dir, op := quote(order), " ASC", ">"
	if order == "_rowid" {
		ord = "rowid"
	}
	if q.Get("desc") == "1" || q.Get("desc") == "true" {
		dir, op = " DESC", "<"
	}
	if after := q.Get("after"); after != "" {
		var cur []any
		raw, _ := base64.RawURLEncoding.DecodeString(after)
		if decode(strings.NewReader(string(raw)), &cur) != nil || len(cur) != 2 {
			return web.Refuse(http.StatusBadRequest, "bad_after", "Pass after exactly as next_after came back.")
		}
		where = append(where, "("+ord+op+"? OR ("+ord+" = ? AND rowid"+op+"?))")
		args = append(args, arg(cur[0]), arg(cur[0]), arg(cur[1]))
	}
	limit := data.Limit(r)
	res, err := t.Query(r.Context(), "SELECT *, rowid AS _rowid FROM "+quote(t.name)+" WHERE "+strings.Join(where, " AND ")+
		" ORDER BY "+ord+dir+", rowid"+dir+" LIMIT ?", append(args, limit+1)...)
	if err != nil {
		return err
	}
	next := ""
	if len(res.Rows) > limit {
		res.Rows = res.Rows[:limit]
		last := res.Rows[limit-1]
		at := len(last) - 1
		for i, c := range res.Columns {
			if c == order {
				at = i
			}
		}
		b, _ := json.Marshal([]any{last[at], last[len(last)-1]})
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	cols, rows, mine := t.shape(res)
	body := map[string]any{"columns": cols, "rows": rows, "next_after": next}
	if !t.Owner {
		body["mine"] = mine
	}
	web.JSON(w, http.StatusOK, body)
	return nil
}

func rowID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, web.NotFound
	}
	return id, nil
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) error {
	t, err := h.open(r, false)
	if err != nil {
		return err
	}
	defer t.Close()
	id, err := rowID(r)
	if err != nil {
		return err
	}
	cond, args, err := t.Rule(r.Context(), t.name, rules.OpRead, false)
	if err != nil {
		return err
	}
	res, err := t.Query(r.Context(), "SELECT *, rowid AS _rowid FROM "+quote(t.name)+" WHERE rowid = ? AND "+cond, append([]any{id}, args...)...)
	if err != nil {
		return err
	}
	if len(res.Rows) == 0 {
		return web.NotFound
	}
	cols, rows, mine := t.shape(res)
	body := map[string]any{"columns": cols, "row": rows[0]}
	if !t.Owner {
		body["mine"] = mine[0]
	}
	web.JSON(w, http.StatusOK, body)
	return nil
}

// values reads the row's columns from the body, refusing unknown ones, and
// stamps who added it and when: a caller who is not the owner cannot set
// visitor_id, created_at, updated_at or written_by. Those server columns are
// dropped, not refused, where the table has none: pages send them (the old
// service took such rows too).
func (t *table) values(r *http.Request, add bool) (map[string]any, error) {
	var in map[string]any
	if err := decode(io.LimitReader(r.Body, 1<<20), &in); err != nil {
		return nil, web.Refuse(http.StatusBadRequest, "bad_json", "Send the row as a JSON object of column names and values.")
	}
	vals := map[string]any{}
	for col, v := range in {
		server := col == "visitor_id" || col == "created_at" || col == "updated_at" || col == "written_by"
		if !t.cols[col] {
			if server {
				continue
			}
			return nil, web.Refuse(http.StatusBadRequest, "bad_column", "The table has no column "+col+".")
		}
		if t.Owner || !server {
			vals[col] = arg(v)
		}
	}
	stamp := func(col string, v any) {
		if _, given := vals[col]; t.cols[col] && !given {
			vals[col] = v
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if add {
		if id := web.CallerOf(r).ID; id != "" {
			stamp("visitor_id", id)
		}
		stamp("created_at", now)
	} else {
		stamp("updated_at", now)
	}
	if len(vals) == 0 && !add {
		return nil, web.Refuse(http.StatusBadRequest, "bad_json", "Send at least one column to change.")
	}
	return vals, nil
}

// passes is whether the row meets the condition.
func (t *table) passes(ctx context.Context, id int64, cond string, args []any) (bool, error) {
	res, err := t.Query(ctx, "SELECT count(*) FROM "+quote(t.name)+" WHERE rowid = ? AND "+cond, append([]any{id}, args...)...)
	return err == nil && res.Rows[0][0].(int64) > 0, err
}

func (h *handler) add(w http.ResponseWriter, r *http.Request) error {
	t, err := h.open(r, true)
	if err != nil {
		return err
	}
	defer t.Close()
	check, cargs, err := t.Rule(r.Context(), t.name, rules.OpAdd, true)
	if err != nil {
		return err
	}
	vals, err := t.values(r, true)
	if err != nil {
		return err
	}
	stmt, args := "INSERT INTO "+quote(t.name)+" DEFAULT VALUES", []any{}
	if len(vals) > 0 {
		var cols, marks []string
		for col, v := range vals {
			cols, marks, args = append(cols, quote(col)), append(marks, "?"), append(args, v)
		}
		stmt = "INSERT INTO " + quote(t.name) + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(marks, ", ") + ")"
	}
	var id int64
	err = t.Tx(r.Context(), func() error {
		res, err := t.Exec(r.Context(), stmt, args...)
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
		if ok, err := t.passes(r.Context(), id, check, cargs); err != nil || ok {
			return err
		}
		return t.Refused(r.Context(), t.name, rules.OpAdd)
	})
	if err == nil {
		err = h.a.Data.Changed(r.Context(), t.Site, t.Name, t.name, "add")
	}
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]any{"changes": 1, "last_insert_id": id})
	return nil
}

func (h *handler) edit(w http.ResponseWriter, r *http.Request) error {
	t, err := h.open(r, true)
	if err != nil {
		return err
	}
	defer t.Close()
	id, err := rowID(r)
	if err != nil {
		return err
	}
	cond, args, err := t.Visible(r.Context(), t.name, rules.OpEdit)
	if err != nil {
		return err
	}
	check, cargs, err := t.Rule(r.Context(), t.name, rules.OpEdit, true)
	if err != nil {
		return err
	}
	vals, err := t.values(r, false)
	if err != nil {
		return err
	}
	var sets []string
	var setArgs []any
	for col, v := range vals {
		sets, setArgs = append(sets, quote(col)+" = ?"), append(setArgs, v)
	}
	err = t.Tx(r.Context(), func() error {
		res, err := t.Exec(r.Context(), "UPDATE "+quote(t.name)+" SET "+strings.Join(sets, ", ")+" WHERE rowid = ? AND "+cond,
			append(append(setArgs, id), args...)...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return web.NotFound
		}
		if ok, err := t.passes(r.Context(), id, check, cargs); err != nil || ok {
			return err
		}
		return t.Refused(r.Context(), t.name, rules.OpEdit)
	})
	if err == nil {
		err = h.a.Data.Changed(r.Context(), t.Site, t.Name, t.name, "edit")
	}
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]any{"changes": 1})
	return nil
}

func (h *handler) remove(w http.ResponseWriter, r *http.Request) error {
	t, err := h.open(r, true)
	if err != nil {
		return err
	}
	defer t.Close()
	id, err := rowID(r)
	if err != nil {
		return err
	}
	cond, args, err := t.Visible(r.Context(), t.name, rules.OpDelete)
	if err != nil {
		return err
	}
	res, err := t.Exec(r.Context(), "DELETE FROM "+quote(t.name)+" WHERE rowid = ? AND "+cond, append([]any{id}, args...)...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return web.NotFound
	}
	if err := h.a.Data.Changed(r.Context(), t.Site, t.Name, t.name, "delete"); err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]any{"deleted": true})
	return nil
}

// decode reads one JSON value, keeping numbers exact.
func decode(r io.Reader, v any) error {
	d := json.NewDecoder(r)
	d.UseNumber()
	return d.Decode(v)
}

// arg is a JSON value as SQLite stores it: whole numbers as integers,
// true and false as 1 and 0, lists and objects as JSON text.
func arg(v any) any {
	switch x := v.(type) {
	case nil, string:
		return x
	case bool:
		if x {
			return int64(1)
		}
		return int64(0)
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		f, _ := x.Float64()
		return f
	}
	b, _ := json.Marshal(v)
	return string(b)
}
