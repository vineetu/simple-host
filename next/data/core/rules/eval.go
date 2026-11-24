package rules

import "strings"

// EvalUsing and EvalCheck are the Go oracle. They evaluate the normalized rule
// tree directly, without inspecting SQL or its arguments.
func (p *Plan) EvalUsing(op Op, v Values, row Row) (bool, error) {
	return p.evaluate(op, false, v, row)
}
func (p *Plan) EvalCheck(op Op, v Values, row Row) (bool, error) { return p.evaluate(op, true, v, row) }
func (p *Plan) evaluate(op Op, check bool, v Values, row Row) (bool, error) {
	x, err := p.expression(op, check)
	if err != nil {
		return false, err
	}
	if err = p.validateValues(op, v); err != nil {
		return false, err
	}
	return eval(x, v, row), nil
}
func operand(x *Expr, v Values, row Row) Value {
	switch x.op {
	case "literal":
		return x.value
	case "binding":
		return v[x.name]
	case "column":
		return row[x.name]
	case "lower":
		a := operand(x.left, v, row)
		if a.Kind != KindText {
			return Null()
		}
		return Text(asciiLower(a.Text))
	}
	return Null()
}
func hasColumn(x *Expr) bool {
	found := false
	walk(x, func(x *Expr) {
		if x.op == "column" {
			found = true
		}
	})
	return found
}
func numeric(v Value) Value {
	if v.Kind == KindBool {
		if v.Bool {
			return Int(1)
		}
		return Int(0)
	}
	return v
}
func isNumber(v Value) bool { return v.Kind == KindInt || v.Kind == KindFloat }

// intFloatEqual follows SQLite's integer/real comparison, preserving the low
// integer bits that a conversion of the integer to float would discard.
func intFloatEqual(i int64, f float64) bool {
	if f < -9223372036854775808.0 || f >= 9223372036854775808.0 {
		return false
	}
	y := int64(f)
	return i == y && float64(i) == f
}

// equal returns comparability separately: rewritten negation must also refuse
// NULL, blobs, and incompatible storage classes.
func equal(a, b Value) (bool, bool) {
	if a.Kind == KindNull || b.Kind == KindNull {
		return false, false
	}
	if isNumber(a) && isNumber(b) {
		switch {
		case a.Kind == KindInt && b.Kind == KindInt:
			return a.Int == b.Int, true
		case a.Kind == KindFloat && b.Kind == KindFloat:
			return a.Float == b.Float, true
		case a.Kind == KindInt:
			return intFloatEqual(a.Int, b.Float), true
		default:
			return intFloatEqual(b.Int, a.Float), true
		}
	}
	if a.Kind != b.Kind {
		return false, false
	}
	switch a.Kind {
	case KindText:
		return a.Text == b.Text, true
	case KindBool:
		return a.Bool == b.Bool, true
	}
	return false, false
}
func listValues(x *Expr, v Values) []Value {
	if x.op == "binding" {
		items := v[x.name].List
		out := make([]Value, len(items))
		for i, s := range items {
			out[i] = Text(s)
		}
		return out
	}
	out := make([]Value, len(x.items))
	for i, item := range x.items {
		out[i] = item.value
	}
	return out
}
func eval(x *Expr, v Values, row Row) bool {
	switch x.op {
	case "&&":
		return eval(x.left, v, row) && eval(x.right, v, row)
	case "||":
		return eval(x.left, v, row) || eval(x.right, v, row)
	case "literal":
		return x.value.Bool
	case "binding":
		return v[x.name].Bool
	case "not bool":
		return !v[x.left.name].Bool
	}
	a := operand(x.left, v, row)
	switch x.op {
	case "is":
		return a.Kind == KindNull
	case "is not":
		return a.Kind != KindNull
	case "==", "!=":
		b := operand(x.right, v, row)
		if hasColumn(x) {
			a, b = numeric(a), numeric(b)
		}
		eq, ok := equal(a, b)
		return ok && (eq == (x.op == "=="))
	case "contains", "not contains":
		b := operand(x.right, v, row)
		if a.Kind != KindText || b.Kind != KindText {
			return false
		}
		return strings.Contains(a.Text, b.Text) == (x.op == "contains")
	case "in", "not in":
		items := listValues(x.right, v)
		textList := x.right.op == "binding" || len(items) > 0 && items[0].Kind == KindText
		if textList && a.Kind != KindText || !textList && !isNumber(a) {
			return false
		}
		found := false
		for _, b := range items {
			eq, _ := equal(a, b)
			if eq {
				found = true
				break
			}
		}
		return found == (x.op == "in")
	}
	return false
}
