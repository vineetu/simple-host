package rules

// Compile validates syntax, resolves schema names, and bounds every composed
// predicate with maximal list bindings before returning an immutable plan.
func Compile(set PolicySet, t Table, b BindingSchema, lim Limits) (*Plan, []RuleError) {
	errs := ValidateSyntax(set, b, lim)
	if t.RowKey != "rowid" && t.RowKey != "_rowid_" && t.RowKey != "oid" && t.RowKey != `"key"` {
		errs = append(errs, *refusal("shape", -1, "unsupported row key"))
	}
	if len(errs) > 0 {
		for i := range errs {
			errs[i].Table = t.Name
		}
		return nil, errs
	}
	p := &Plan{table: t.Name, schema: make(BindingSchema, len(b)), lim: lim}
	for name, decl := range b {
		p.schema[name] = decl
	}
	columns := map[string]string{}
	for _, c := range t.Columns {
		columns[asciiLower(c.Name)] = c.Name
	}
	for _, pol := range set.Policies {
		pp := plannedPolicy{command: pol.Command, restrictive: pol.Restrictive}
		for _, clause := range []struct {
			text string
			dest **Expr
		}{{pol.Using, &pp.using}, {pol.Check, &pp.check}} {
			if clause.text == "" {
				continue
			}
			x, _ := Parse(clause.text, lim) // ValidateSyntax already parsed and typed it.
			if clause.dest == &pp.using {
				pp.shapeUsing = x
			}
			walk(x, func(x *Expr) {
				if x.op == "column" {
					if name, ok := columns[asciiLower(x.name)]; ok {
						x.name = name
					} else {
						errs = append(errs, RuleError{Table: t.Name, Policy: pol.Name, Clause: "column", Position: x.pos, Message: "unknown column " + x.name})
					}
				}
			})
			*clause.dest = normal(x, false)
		}
		if pp.check == nil && (pol.Command == CmdEdit || pol.Command == CmdAll) {
			pp.check = pp.using
		}
		p.policies = append(p.policies, pp)
	}
	if len(errs) > 0 {
		return nil, errs
	}
	for _, op := range []Op{OpRead, OpAdd, OpEdit, OpDelete} {
		for _, check := range []bool{false, true} {
			if check && op != OpAdd && op != OpEdit || !check && op == OpAdd {
				continue
			}
			x := p.compose(op, check)
			if check {
				p.check[op] = x
			} else {
				p.using[op] = x
			}
			if _, err := p.predicate(x, nil, true); err != nil {
				errs = append(errs, err.(RuleError))
			}
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return p, nil
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
