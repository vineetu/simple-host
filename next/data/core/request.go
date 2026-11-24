package data

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"simplehost/core/web"
	"simplehost/data/core/rules"
	sites "simplehost/sites/core"
)

// Req is one data request from a page, a person or their AI app: the open
// namespace and whether the caller passes every rule.
type Req struct {
	*NS
	r *http.Request
	// Owner: the site's owner, a member of the owning team, or the operator.
	Owner bool
	// Checked: the request carried a spam check that passed.
	Checked bool
}

// Begin opens the namespace a data route names ({ns}) on the request's
// site. The owner passes; anyone else first passes the visit decision. A
// write runs the spam check when it carries X-SH-Turnstile (a forged write
// never gets here: access.Caller refuses it). Close it.
func (d *Data) Begin(r *http.Request, s *sites.Sites, write bool) (*Req, error) {
	site, err := s.Of(r)
	if err != nil {
		return nil, err
	}
	owner, err := IsOwner(r, s, site)
	if err != nil {
		return nil, err
	}
	if !owner {
		dec, err := s.Visit(r, site)
		if err != nil {
			return nil, err
		}
		if err := dec.Err(); err != nil {
			return nil, err
		}
	}
	ns, err := d.Open(r, site, r.PathValue("ns"))
	if err != nil {
		return nil, err
	}
	q := &Req{NS: ns, r: r, Owner: owner}
	if tok := r.Header.Get("X-SH-Turnstile"); write && tok != "" && ns.SpamCheck {
		if err := d.Turnstile(r, tok); err != nil {
			ns.Close()
			return nil, err
		}
		q.Checked = true
	}
	return q, nil
}

// IsOwner is whether the caller may manage the site (sites.Manage without
// its refusal).
func IsOwner(r *http.Request, s *sites.Sites, site *sites.Site) (bool, error) {
	err := s.Manage(r, site)
	var ref *web.Refusal
	if errors.As(err, &ref) {
		return false, nil
	}
	return err == nil, err
}

// Rule is the SQL condition, with its arguments, a row must meet for this
// caller to do op to table: the row filter, or with check the add or edit
// check on the written row. The owner's is "1". A caller the rules leave out
// entirely is refused here.
func (q *Req) Rule(ctx context.Context, table string, op rules.Op, check bool) (string, []any, error) {
	if q.Owner {
		return "1", nil, nil
	}
	plan, err := q.Plan(ctx, table)
	if err != nil {
		return "", nil, err
	}
	if plan == nil {
		return "", nil, q.refusal(nil, op)
	}
	b := Bindings(q.r, false, q.Checked)
	var p rules.Predicate
	if check {
		p, err = plan.Check(op, b)
	} else {
		p, err = plan.Using(op, b)
	}
	if err != nil {
		return "", nil, web.Refuse(http.StatusBadRequest, "bad_rules", err.Error())
	}
	if p.SQL == "0" {
		return "", nil, q.refusal(plan, op)
	}
	args := make([]any, len(p.Args))
	for i, v := range p.Args {
		args[i] = sqlValue(v)
	}
	return "(" + p.SQL + ")", args, nil
}

// Refused is the refusal for a caller the rules leave out of op on table.
func (q *Req) Refused(ctx context.Context, table string, op rules.Op) error {
	plan, err := q.Plan(ctx, table)
	if err != nil {
		return err
	}
	return q.refusal(plan, op)
}

func (q *Req) refusal(plan *rules.Plan, op rules.Op) error {
	switch {
	case plan != nil && op == rules.OpAdd && !q.Checked && q.SpamCheck && plan.ReferencesBinding(rules.OpAdd, "auth.checked"):
		return web.Refuse(http.StatusForbidden, "spam_check_required", "Pass the spam check first: send its token as X-SH-Turnstile.")
	case web.CallerOf(q.r).ID == "":
		return web.Refuse(http.StatusUnauthorized, "sign_in_required", "Sign in on this site first, or ask the owner to open this data.")
	}
	return web.Refuse(http.StatusForbidden, "not_allowed", "The site's rules do not let you do this. Ask the site's owner.")
}

func sqlValue(v rules.Value) any {
	switch v.Kind {
	case rules.KindBool:
		if v.Bool {
			return int64(1)
		}
		return int64(0)
	case rules.KindInt:
		return v.Int
	case rules.KindFloat:
		return v.Float
	case rules.KindText:
		return v.Text
	case rules.KindBlob:
		return []byte(v.Text)
	}
	return nil
}

// Tx runs fn's statements on this namespace as one transaction: all of them
// or none. (The namespace has one connection, so they all run inside it.)
func (n *NS) Tx(ctx context.Context, fn func() error) error {
	if _, err := n.db.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return guard(err)
	}
	if err := fn(); err != nil {
		n.db.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		return err
	}
	_, err := n.db.ExecContext(ctx, "COMMIT")
	return guard(err)
}

// Author is what this caller sees of a row's visitor_id: the owner sees
// every one, anyone else only their own. mine: the row is the caller's.
func (q *Req) Author(v any) (any, bool) {
	id, _ := v.(string)
	mine := id != "" && id == web.CallerOf(q.r).ID
	if q.Owner || mine {
		return v, mine
	}
	return nil, false
}

// Limit is the page size a list asks for: ?limit= from 1 to 500, else 100.
func Limit(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n < 1 || n > 500 {
		return 100
	}
	return n
}

// Visible is the condition for rows the caller may both read and do op
// (edit or delete) to.
func (q *Req) Visible(ctx context.Context, table string, op rules.Op) (string, []any, error) {
	read, rargs, err := q.Rule(ctx, table, rules.OpRead, false)
	if err != nil {
		return "", nil, err
	}
	cond, args, err := q.Rule(ctx, table, op, false)
	return read + " AND " + cond, append(rargs, args...), err
}
