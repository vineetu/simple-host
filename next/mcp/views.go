package mcp

import (
	"fmt"

	"simplehost/core/web"
)

// Views reshape a route's answer for the AI app (a tool's view, tools.go).
// The routes answer the site's own pages, which need a visitor's id to tell
// their own entries apart; the model never needs an internal id of a person,
// a team or an invitation, so it gets a mine flag instead, and people's
// emails only when it asks for them.

// without drops fields from the answer.
func without(keys ...string) func(*call, map[string]any) map[string]any {
	return func(_ *call, v map[string]any) map[string]any {
		for _, k := range keys {
			delete(v, k)
		}
		return v
	}
}

// siteView is a site's answer with the named people counted, and their
// emails only when include_emails is true.
func siteView(c *call, v map[string]any) map[string]any {
	viewers, ok := v["viewers"].([]any)
	if !ok {
		return v
	}
	v["viewer_count"] = len(viewers)
	if c.args["include_emails"] != true {
		delete(v, "viewers")
	}
	return v
}

// mineView: each value or stored file (items, or the one answered) says
// whether it is the caller's, in place of who saved it.
func mineView(c *call, v map[string]any) map[string]any {
	me := web.CallerOf(c.r).ID
	mark := func(m map[string]any) {
		id, _ := m["visitor_id"].(string)
		if _, ok := m["mine"]; !ok {
			m["mine"] = id != "" && id == me
		}
		delete(m, "visitor_id")
	}
	if items, ok := v["items"].([]any); ok {
		for _, it := range items {
			if m, ok := it.(map[string]any); ok {
				mark(m)
			}
		}
		return v
	}
	mark(v)
	return v
}

// rowsView: a visitor_id column shows "you" or "visitor 1", "visitor 2"...
// (numbered within this answer, so one person's rows can be told apart),
// and list_rows and read_row always say which rows are the caller's.
func rowsView(c *call, v map[string]any) map[string]any {
	me := web.CallerOf(c.r).ID
	col := -1
	cols, _ := v["columns"].([]any)
	for i, name := range cols {
		if name == "visitor_id" {
			col = i
		}
	}
	rows, _ := v["rows"].([]any)
	one := false
	if row, ok := v["row"].([]any); ok {
		rows, one = []any{row}, true
	}
	_, hasMine := v["mine"]
	_, isRows := v["next_after"]
	labels, mine, n := map[string]string{}, []any{}, 0
	for _, r := range rows {
		row, _ := r.([]any)
		m := false
		if col >= 0 && col < len(row) {
			if id, ok := row[col].(string); ok && id != "" {
				m = id == me
				if labels[id] == "" && m {
					labels[id] = "you"
				} else if labels[id] == "" {
					n++
					labels[id] = fmt.Sprintf("visitor %d", n)
				}
				row[col] = labels[id]
			}
		}
		mine = append(mine, m)
	}
	switch {
	case hasMine: // a visitor's answer says already
	case one:
		v["mine"] = len(mine) > 0 && mine[0] == true
	case isRows:
		v["mine"] = mine
	}
	return v
}
