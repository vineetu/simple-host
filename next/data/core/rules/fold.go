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
	switch {
	case x.op == "&&" || x.op == "||":
		return p.junction(x, v, worst)
	case hasColumn(x):
		return p.comparisonSQL(x, v, worst)
	case worst && hasBinding(x):
		// The worst case keeps binding-dependent branches as SQL so they are sized.
		return fragment{Predicate: Predicate{SQL: "1"}}
	}
	return constant(eval(x, v, nil))
}

// junction emits AND/OR, folding a constant side: the absorbing value wins
// and the identity value yields the other side.
func (p *Plan) junction(x *Expr, v Values, worst bool) fragment {
	a, b := p.emit(x.left, v, worst), p.emit(x.right, v, worst)
	absorbing, op := "0", " AND "
	if x.op == "||" {
		absorbing, op = "1", " OR "
	}
	for _, pair := range [][2]fragment{{a, b}, {b, a}} {
		if c, other := pair[0], pair[1]; c.constant {
			if c.SQL == absorbing {
				return c
			}
			return other
		}
	}
	return fragment{Predicate: Predicate{SQL: "(" + a.SQL + op + b.SQL + ")", Args: append(a.Args, b.Args...)}}
}

func hasBinding(x *Expr) bool {
	found := false
	walk(x, func(x *Expr) { found = found || x.op == "binding" })
	return found
}

var nullSQL = map[string]string{"is": " IS NULL", "is not": " IS NOT NULL"}

// comparisonSQL emits a column comparison guarded by the column's storage type,
// so text never matches a number and NULL bound values match nothing.
func (p *Plan) comparisonSQL(x *Expr, v Values, worst bool) fragment {
	left, right := x.left, x.right
	if (x.op == "==" || x.op == "!=") && !hasColumn(left) {
		left, right = right, left
	}
	col := left
	if col.op == "lower" {
		col = col.left
	}
	quoted := `"` + strings.ReplaceAll(col.name, `"`, `""`) + `"`
	if sql, ok := nullSQL[x.op]; ok {
		return fragment{Predicate: Predicate{SQL: quoted + sql}}
	}
	target := quoted
	if left.op == "lower" {
		target = "lower(" + quoted + ")"
	}
	args := p.comparisonArgs(x.op, right, v, worst)
	text := len(args) == 0 || args[0].Kind == KindText
	guard := "typeof(" + quoted + ") IN ('integer','real')"
	if text {
		guard = "typeof(" + quoted + ") = 'text'"
	}
	switch {
	case len(args) == 0 && x.op == "in", len(args) > 0 && args[0].Kind == KindNull:
		return constant(false)
	case len(args) == 0:
		return fragment{Predicate: Predicate{SQL: "(" + guard + ")"}}
	}
	return fragment{Predicate: Predicate{SQL: "(" + guard + " AND " + condition(x.op, target, text, len(args)) + ")", Args: args}}
}

// comparisonArgs binds the right-hand side; the worst case fills list bindings to the limit.
func (p *Plan) comparisonArgs(op string, right *Expr, v Values, worst bool) []Value {
	if op != "in" && op != "not in" {
		return []Value{numeric(p.boundValue(right, v, worst))}
	}
	if !worst || right.op != "binding" {
		return listValues(right, v)
	}
	args := make([]Value, p.lim.ListItems)
	for i := range args {
		args[i] = Text("")
	}
	return args
}

// condition is the SQL test for op against n placeholders.
func condition(op, target string, text bool, n int) string {
	collate := ""
	if text {
		collate = " COLLATE BINARY"
	}
	placeholders := "(" + strings.TrimSuffix(strings.Repeat("?,", n), ",") + ")"
	switch op {
	case "==":
		return target + " = ?" + collate
	case "!=":
		return target + " != ?" + collate
	case "in":
		return target + collate + " IN " + placeholders
	case "not in":
		return target + collate + " NOT IN " + placeholders
	case "contains":
		return "instr(" + target + ", ?) > 0"
	}
	return "instr(" + target + ", ?) = 0"
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
