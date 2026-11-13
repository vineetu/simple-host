package rules

import "strings"

// Using returns only this command's filter; callers combine read visibility
// themselves for edit/delete. Check tests the resulting written row.
func (p *Plan) Using(op Op, v Values) (Predicate, error) { return p.forRequest(op, false, v) }
func (p *Plan) Check(op Op, v Values) (Predicate, error) { return p.forRequest(op, true, v) }
func (p *Plan) forRequest(op Op, check bool, v Values) (Predicate, error) {
	x, err := p.expression(op, check)
	if err != nil {
		return Predicate{}, err
	}
	if err = p.validateValues(op, v); err != nil {
		return Predicate{}, err
	}
	return p.predicate(x, v, false)
}

type fragment struct {
	Predicate
	constant bool
}

func constant(b bool) fragment {
	s := "0"
	if b {
		s = "1"
	}
	return fragment{Predicate: Predicate{SQL: s}, constant: true}
}
func (p *Plan) predicate(x *Expr, v Values, worst bool) (Predicate, error) {
	f := p.emit(x, v, worst)
	if len(f.SQL) > p.lim.PredicateBytes || len(f.Args) > p.lim.PredicateArgs {
		return Predicate{}, RuleError{Table: p.table, Clause: "composed", Position: -1, Message: "composed predicate exceeds SQL or argument limit"}
	}
	return f.Predicate, nil
}
func (p *Plan) emit(x *Expr, v Values, worst bool) fragment {
	if x.op == "&&" || x.op == "||" {
		a, b := p.emit(x.left, v, worst), p.emit(x.right, v, worst)
		for i := 0; i < 2; i++ {
			if a.constant {
				if x.op == "&&" {
					if a.SQL == "0" {
						return a
					}
					return b
				}
				if a.SQL == "1" {
					return a
				}
				return b
			}
			a, b = b, a
		}
		op := " AND "
		if x.op == "||" {
			op = " OR "
		}
		return fragment{Predicate: Predicate{SQL: "(" + a.SQL + op + b.SQL + ")", Args: append(a.Args, b.Args...)}}
	}
	if !hasColumn(x) {
		if worst {
			binding := false
			walk(x, func(x *Expr) {
				if x.op == "binding" {
					binding = true
				}
			})
			if binding {
				return fragment{Predicate: Predicate{SQL: "1"}}
			}
		}
		return constant(eval(x, v, nil))
	}
	left, right := x.left, x.right
	if (x.op == "==" || x.op == "!=") && !hasColumn(left) {
		left, right = right, left
	}
	col := left
	if col.op == "lower" {
		col = col.left
	}
	quoted := `"` + strings.ReplaceAll(col.name, `"`, `""`) + `"`
	if x.op == "is" || x.op == "is not" {
		op := " IS NULL"
		if x.op == "is not" {
			op = " IS NOT NULL"
		}
		return fragment{Predicate: Predicate{SQL: quoted + op}}
	}
	target := quoted
	if left.op == "lower" {
		target = "lower(" + quoted + ")"
	}
	var args []Value
	if x.op == "in" || x.op == "not in" {
		if worst && right.op == "binding" {
			args = make([]Value, p.lim.ListItems)
			for i := range args {
				args[i] = Text("")
			}
		} else {
			args = listValues(right, v)
		}
	} else {
		args = []Value{numeric(p.boundValue(right, v, worst))}
	}
	textType := len(args) == 0 || args[0].Kind == KindText
	guard := "typeof(" + quoted + ") IN ('integer','real')"
	if textType {
		guard = "typeof(" + quoted + ") = 'text'"
	}
	if len(args) == 0 {
		if x.op == "in" {
			return constant(false)
		}
		return fragment{Predicate: Predicate{SQL: "(" + guard + ")"}}
	}
	if args[0].Kind == KindNull {
		return constant(false)
	}
	var condition string
	switch x.op {
	case "==", "!=":
		op := " = "
		if x.op == "!=" {
			op = " != "
		}
		condition = target + op + "?"
		if textType {
			condition += " COLLATE BINARY"
		}
	case "in", "not in":
		if textType {
			target += " COLLATE BINARY"
		}
		op := " IN "
		if x.op == "not in" {
			op = " NOT IN "
		}
		condition = target + op + "(" + strings.TrimSuffix(strings.Repeat("?,", len(args)), ",") + ")"
	case "contains", "not contains":
		op := " > 0"
		if x.op == "not contains" {
			op = " = 0"
		}
		condition = "instr(" + target + ", ?)" + op
	}
	return fragment{Predicate: Predicate{SQL: "(" + guard + " AND " + condition + ")", Args: args}}
}
func (p *Plan) boundValue(x *Expr, v Values, worst bool) Value {
	if !worst {
		return operand(x, v, nil)
	}
	if x.op == "binding" {
		switch p.schema[x.name].Type {
		case TypeBool:
			return Bool(false)
		case TypeNumber:
			return Int(0)
		case TypeText:
			return Text("")
		}
	}
	if x.op == "lower" {
		a := p.boundValue(x.left, v, true)
		if a.Kind == KindText {
			return Text(asciiLower(a.Text))
		}
		return Null()
	}
	return x.value
}
