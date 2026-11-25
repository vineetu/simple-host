package rules

import "maps"

// Compile validates syntax, resolves schema names, and bounds every composed
// predicate with maximal list bindings before returning an immutable plan.
func Compile(set PolicySet, t Table, b BindingSchema, lim Limits) (*Plan, []RuleError) {
	errs := ValidateSyntax(set, b, lim)
	if !rowKeys[t.RowKey] {
		errs = append(errs, *refusal("shape", -1, "unsupported row key"))
	}
	if len(errs) > 0 {
		for i := range errs {
			errs[i].Table = t.Name
		}
		return nil, errs
	}
	p := &Plan{table: t.Name, schema: make(BindingSchema, len(b)), lim: lim}
	maps.Copy(p.schema, b)
	columns := map[string]string{}
	for _, c := range t.Columns {
		columns[asciiLower(c.Name)] = c.Name
	}
	for _, pol := range set.Policies {
		using, usingErrs := resolveClause(pol.Using, lim, columns)
		check, checkErrs := resolveClause(pol.Check, lim, columns)
		for _, e := range append(usingErrs, checkErrs...) {
			e.Table, e.Policy = t.Name, pol.Name
			errs = append(errs, e)
		}
		pp := plannedPolicy{command: pol.Command, restrictive: pol.Restrictive, using: normal(using, false), check: normal(check, false)}
		if pp.check == nil && (pol.Command == CmdEdit || pol.Command == CmdAll) {
			pp.check = pp.using
		}
		p.policies = append(p.policies, pp)
	}
	if len(errs) > 0 {
		return nil, errs
	}
	for _, c := range composedClauses {
		x := p.compose(c.op, c.check)
		if c.check {
			p.check[c.op] = x
		} else {
			p.using[c.op] = x
		}
		if _, err := p.predicate(x, nil, true); err != nil {
			errs = append(errs, err.(RuleError))
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return p, nil
}

var rowKeys = map[string]bool{"rowid": true, "_rowid_": true, "oid": true, `"key"`: true}

// composedClauses are the (op, clause) pairs a plan stores: add has only a
// check, edit has both, read and delete only a using.
var composedClauses = []struct {
	op    Op
	check bool
}{{OpRead, false}, {OpAdd, true}, {OpEdit, false}, {OpEdit, true}, {OpDelete, false}}

// resolveClause parses an already-validated clause and rewrites column names
// to the table's spelling; an empty clause is nil.
func resolveClause(text string, lim Limits, columns map[string]string) (*Expr, []RuleError) {
	if text == "" {
		return nil, nil
	}
	x, _ := Parse(text, lim) // ValidateSyntax already parsed and typed it.
	var errs []RuleError
	walk(x, func(x *Expr) {
		if x.op != "column" {
			return
		}
		if name, ok := columns[asciiLower(x.name)]; ok {
			x.name = name
		} else {
			errs = append(errs, RuleError{Clause: "column", Position: x.pos, Message: "unknown column " + x.name})
		}
	})
	return x, errs
}
func boolean(b bool) *Expr { return &Expr{op: "literal", value: Bool(b)} }
func join(op string, a, b *Expr) *Expr {
	if a == nil {
		return b
	}
	return &Expr{op: op, left: a, right: b}
}
func (p *Plan) compose(op Op, check bool) *Expr {
	var allow, restrict *Expr
	for _, pol := range p.policies {
		if !applies(pol.command, op) {
			continue
		}
		x := pol.using
		if check {
			x = pol.check
		}
		if pol.restrictive {
			restrict = join("&&", restrict, x)
		} else {
			allow = join("||", allow, x)
		}
	}
	if allow == nil {
		return boolean(false)
	}
	if restrict == nil {
		return allow
	}
	return join("&&", allow, restrict)
}

// normal pushes negation to atoms; it does not complement malformed/NULL values.
func normal(x *Expr, neg bool) *Expr {
	if x == nil {
		return nil
	}
	if x.op == "group" {
		return normal(x.left, neg)
	}
	if x.op == "!" {
		return normal(x.left, !neg)
	}
	y := *x
	if x.op == "&&" || x.op == "||" {
		if neg {
			if x.op == "&&" {
				y.op = "||"
			} else {
				y.op = "&&"
			}
		}
		y.left, y.right = normal(x.left, neg), normal(x.right, neg)
		return &y
	}
	if neg {
		switch x.op {
		case "==":
			y.op = "!="
		case "!=":
			y.op = "=="
		case "in":
			y.op = "not in"
		case "contains":
			y.op = "not contains"
		case "is":
			y.op = "is not"
		case "is not":
			y.op = "is"
		case "binding":
			return &Expr{op: "not bool", left: x}
		case "literal":
			y.value = Bool(!x.value.Bool)
		}
	}
	return &y
}
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
