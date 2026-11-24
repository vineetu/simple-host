package rules

// ReadIsOwnOrOwner recognizes the structural author/owner shape, including a
// split across permissive policies. Restrictive policies can only narrow it.
func (p *Plan) ReadIsOwnOrOwner() bool {
	found, own := false, false
	for _, pol := range p.policies {
		if pol.restrictive || !applies(pol.command, OpRead) {
			continue
		}
		found = true
		ok, hasOwn := ownAlternatives(pol.shapeUsing)
		if !ok {
			return false
		}
		own = own || hasOwn
	}
	return found && own
}
func ownAlternatives(x *Expr) (bool, bool) {
	x = ungroup(x)
	if x.op == "||" {
		a, ao := ownAlternatives(x.left)
		b, bo := ownAlternatives(x.right)
		return a && b, ao || bo
	}
	return ownTerm(x)
}
func ownTerm(x *Expr) (bool, bool) {
	x = ungroup(x)
	if x.op == "&&" {
		a, ao := ownTerm(x.left)
		b, bo := ownTerm(x.right)
		return a || b, ao || bo
	}
	if x.op != "==" {
		return false, false
	}
	a, b := x.left, x.right
	for i := 0; i < 2; i++ {
		if a.op == "binding" && a.name == "auth.id" && b.op == "column" && asciiLower(b.name) == "visitor_id" {
			return true, true
		}
		if a.op == "binding" && a.name == "auth.role" && b.op == "literal" && b.value.Kind == KindText && b.value.Text == "owner" {
			return true, false
		}
		a, b = b, a
	}
	return false, false
}
