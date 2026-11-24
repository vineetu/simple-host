package rules

// Partial is for refusal classification, never authorization. A column or an
// absent binding makes its entire atom unknown even when another operand is NULL.
func (p *Plan) Partial(op Op, v Values) Tri {
	x, err := p.expression(op, op == OpAdd)
	if err != nil {
		return False
	}
	return p.partial(x, v)
}
func (p *Plan) partial(x *Expr, v Values) Tri {
	if x.op == "&&" || x.op == "||" {
		a, b := p.partial(x.left, v), p.partial(x.right, v)
		if x.op == "&&" {
			if a == False || b == False {
				return False
			}
			if a == True && b == True {
				return True
			}
		} else {
			if a == True || b == True {
				return True
			}
			if a == False && b == False {
				return False
			}
		}
		return Unknown
	}
	unknown := false
	walk(x, func(x *Expr) {
		if x.op == "column" {
			unknown = true
		}
		if x.op == "binding" {
			value, ok := v[x.name]
			if !ok || !validValue(value, p.schema[x.name], p.lim) {
				unknown = true
			}
		}
	})
	if unknown {
		return Unknown
	}
	if eval(x, v, nil) {
		return True
	}
	return False
}
