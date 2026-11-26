package mcp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

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

// rowsView: list_rows and read_row say which rows are the caller's (any
// visitor_id column holding the caller's id). The ids themselves become
// labels afterwards, like every id in stored data (visitorNames).
func rowsView(c *call, v map[string]any) map[string]any {
	me := web.CallerOf(c.r).ID
	cols, _ := v["columns"].([]any)
	rows, _ := v["rows"].([]any)
	one := false
	if row, ok := v["row"].([]any); ok {
		rows, one = []any{row}, true
	}
	_, hasMine := v["mine"]
	_, isRows := v["next_after"]
	mine := []any{}
	for _, r := range rows {
		row, _ := r.([]any)
		m := false
		for i, name := range cols {
			if name == "visitor_id" && i < len(row) && me != "" && row[i] == me {
				m = true
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

// visitorNames is the one rule for people in stored data. Every tool on a
// namespace answers with each id-shaped value, wherever it is (any column,
// alias, expression, group_concat, JSON, text), as "you" for the caller or
// visitor-<8 hex> for anyone else: an HMAC of the id under a key of the site,
// so one person has one label on that site in every answer, page and tool.
// The labels in a call's values (where, row, params, quoted in SQL) are
// mapped back to the ids first, so filtering, joining and writing by label
// work; a label of nobody in the namespace stays as typed.
type visitorNames struct {
	key []byte
	me  string
}

var (
	idShape    = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	labelShape = regexp.MustCompile(`\bvisitor-[0-9a-f]{8}\b`)
	youInSQL   = regexp.MustCompile(`(?i)'you'`)
)

// newVisitorNames keys the labels by the site's id, under the service's
// passcode key (PASSCODE_ENC_KEY).
func newVisitorNames(c *call) *visitorNames {
	m := hmac.New(sha256.New, []byte(c.h.a.Config.PasscodeKey))
	m.Write([]byte("visitor label v1|" + c.siteID()))
	return &visitorNames{key: m.Sum(nil), me: strings.ToLower(web.CallerOf(c.r).ID)}
}

func (n *visitorNames) label(id string) string {
	id = strings.ToLower(id)
	if id == n.me {
		return "you"
	}
	m := hmac.New(sha256.New, n.key)
	m.Write([]byte(id))
	return "visitor-" + hex.EncodeToString(m.Sum(nil))[:8]
}

// hide labels every id in v, at any depth.
func (n *visitorNames) hide(v any) any {
	switch x := v.(type) {
	case string:
		return idShape.ReplaceAllStringFunc(x, n.label)
	case map[string]any:
		for k, val := range x {
			x[k] = n.hide(val)
		}
	case []any:
		for i := range x {
			x[i] = n.hide(x[i])
		}
	}
	return v
}

// reveal is the call's arguments with labels mapped back to ids: a
// visitor-<hex> anywhere in a string, and "you" as a whole value in where,
// row or params, or quoted in SQL. The namespace's people are read only when
// a visitor label is given (the owner's SQL; for anyone else it stays as typed).
func (n *visitorNames) reveal(c *call) map[string]any {
	var ids map[string]string
	if b, _ := json.Marshal(c.args); labelShape.Match(b) {
		ids = map[string]string{}
		for _, id := range c.namespacePeople() {
			ids[n.label(id)] = id
		}
	}
	var walk func(v any, inner bool) any
	walk = func(v any, inner bool) any {
		switch x := v.(type) {
		case string:
			if inner && x == "you" && n.me != "" {
				return n.me
			}
			return labelShape.ReplaceAllStringFunc(x, func(l string) string {
				if id, ok := ids[l]; ok {
					return id
				}
				return l
			})
		case map[string]any:
			out := map[string]any{}
			for k, val := range x {
				out[k] = walk(val, inner)
			}
			return out
		case []any:
			out := make([]any, len(x))
			for i := range x {
				out[i] = walk(x[i], inner)
			}
			return out
		}
		return v
	}
	out := map[string]any{}
	for k, v := range c.args {
		out[k] = walk(v, k == "where" || k == "row" || k == "params")
	}
	if sql, ok := out["sql"].(string); ok && n.me != "" {
		out["sql"] = youInSQL.ReplaceAllString(sql, "'"+n.me+"'")
	}
	return out
}

// namespacePeople are the ids in the namespace's visitor_id columns, read
// with the caller's rights (the owner's SQL); none when that is refused.
func (c *call) namespacePeople() []string {
	schema, err := c.sql("schema")
	if err != nil {
		return nil
	}
	_, v, err := c.h.do(c.r, "POST", schema, map[string]any{})
	if err != nil {
		return nil
	}
	tables, _ := v.(map[string]any)["tables"].([]any)
	var selects []string
	for _, t := range tables {
		t, _ := t.(map[string]any)
		name, _ := t["name"].(string)
		create, _ := t["sql"].(string)
		if strings.Contains(strings.ToLower(create), "visitor_id") {
			selects = append(selects, `SELECT visitor_id FROM "`+strings.ReplaceAll(name, `"`, `""`)+`" WHERE visitor_id IS NOT NULL`)
		}
	}
	query, err := c.sql("query")
	if err != nil || len(selects) == 0 {
		return nil
	}
	_, v, err = c.h.do(c.r, "POST", query, map[string]any{"sql": strings.Join(selects, " UNION ")})
	if err != nil {
		return nil
	}
	rows, _ := v.(map[string]any)["rows"].([]any)
	var ids []string
	for _, r := range rows {
		if r, ok := r.([]any); ok && len(r) == 1 {
			if id, ok := r[0].(string); ok {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// siteID is the id of the call's site ("" when there is none).
func (c *call) siteID() string {
	p, err := c.site("", nil)
	if err != nil {
		return ""
	}
	u, err := url.Parse(p)
	if err != nil {
		return ""
	}
	name, _ := url.PathUnescape(strings.TrimPrefix(u.Path, "/v1/sites/"))
	req := c.r.Clone(c.r.Context())
	req.URL = u
	req.SetPathValue("site", name)
	s, err := c.h.a.Sites.Of(req)
	if err != nil {
		return ""
	}
	return s.ID
}
