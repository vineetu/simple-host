package rules

import "strings"

// ValidateSyntax validates everything that does not require a table schema.
func ValidateSyntax(set PolicySet, b BindingSchema, lim Limits) []RuleError {
	var errs []RuleError
	if set.Version != 0 && set.Version != 1 {
		errs = append(errs, *refusal("version", -1, "version must be 1"))
	}
	if set.Policies == nil {
		errs = append(errs, *refusal("json", -1, "policies must be an array"))
	}
	if len(set.Policies) > lim.Policies {
		errs = append(errs, *refusal("size", -1, "too many policies"))
	}
	errs = append(errs, duplicateNames(set)...)
	for _, pol := range set.Policies {
		add := func(e *RuleError) {
			if e != nil {
				e.Policy = pol.Name
				errs = append(errs, *e)
			}
		}
		if len(pol.Name) > lim.NameBytes {
			add(refusal("name", -1, "policy name exceeds byte limit"))
		}
		switch pol.Command {
		case CmdRead, CmdAdd, CmdEdit, CmdDelete, CmdAll:
		default:
			add(refusal("command", -1, "unknown command"))
			continue
		}
		var using *Expr
		for _, clause := range []struct {
			name, text          string
			required, forbidden bool
		}{
			{"using", pol.Using, pol.Command != CmdAdd, pol.Command == CmdAdd},
			{"check", pol.Check, pol.Command == CmdAdd, pol.Command == CmdRead || pol.Command == CmdDelete},
		} {
			if clause.text == "" {
				if clause.required {
					add(refusal(clause.name, -1, "clause is required"))
				}
				continue
			}
			if clause.forbidden {
				add(refusal(clause.name, -1, "clause is not allowed for this command"))
				continue
			}
			x, err := Parse(clause.text, lim)
			if err != nil {
				if err.Clause == "using" {
					err.Clause = clause.name
				}
				add(err)
				continue
			}
			v := validator{schema: b, addCheck: pol.Command == CmdAdd && clause.name == "check"}
			if err := v.boolean(x); err != nil {
				add(err)
				continue
			}
			if clause.name == "using" {
				using = x
			}
		}
		if !pol.Restrictive && (pol.Command == CmdEdit || pol.Command == CmdDelete || pol.Command == CmdAll) && using != nil && !guarded(using) {
			add(refusal("anonymous_change", using.pos, "every using alternative must guard changes by identity"))
		}
	}
	return errs
}

type validator struct {
	schema   BindingSchema
	addCheck bool
}
type operandInfo struct {
	typ                       Type
	column, binding, nullable bool
}

func (v validator) operand(x *Expr) (operandInfo, *RuleError) {
	fail := func(msg string) (operandInfo, *RuleError) { return operandInfo{}, refusal("type", x.pos, msg) }
	switch x.op {
	case "column":
		return operandInfo{column: true, nullable: true}, nil
	case "binding":
		b, ok := v.schema[x.name]
		if !ok {
			ns := strings.SplitN(x.name, ".", 2)[0]
			msg := "unknown namespace " + ns
			for name := range v.schema {
				if strings.HasPrefix(name, ns+".") {
					msg = "unknown binding " + x.name
					break
				}
			}
			return operandInfo{}, refusal("binding", x.pos, msg)
		}
		if b.AddCheckOnly && !v.addCheck {
			return operandInfo{}, refusal("binding", x.pos, "binding is only allowed in the check of an add policy")
		}
		return operandInfo{typ: b.Type, binding: true, nullable: b.Nullable}, nil
	case "literal":
		switch x.value.Kind {
		case KindBool:
			return operandInfo{typ: TypeBool}, nil
		case KindText:
			return operandInfo{typ: TypeText}, nil
		case KindInt, KindFloat:
			return operandInfo{typ: TypeNumber}, nil
		default:
			return fail("use is null")
		}
	case "lower":
		if x.left.op == "lower" {
			return fail("lower() cannot be nested")
		}
		i, err := v.operand(x.left)
		if err != nil {
			return i, err
		}
		if i.typ != 0 && i.typ != TypeText {
			return fail("lower() needs text")
		}
		i.typ, i.nullable = TypeText, false
		return i, nil
	default:
		return fail("expected a comparison operand")
	}
}

func (v validator) boolean(x *Expr) *RuleError {
	switch x.op {
	case "group", "!":
		return v.boolean(x.left)
	case "&&", "||":
		if err := v.boolean(x.left); err != nil {
			return err
		}
		return v.boolean(x.right)
	case "==", "!=", "contains", "in", "is", "is not":
		return v.comparison(x)
	default:
		i, err := v.operand(x)
		if err != nil {
			return err
		}
		if i.typ != TypeBool || i.column {
			return refusal("type", x.pos, "a condition must be a comparison or boolean binding/literal")
		}
		return nil
	}
}

func (v validator) comparison(x *Expr) *RuleError {
	a, err := v.operand(x.left)
	if err != nil {
		return err
	}
	fail := func(msg string) *RuleError { return refusal("type", x.pos, msg) }
	if x.op == "is" || x.op == "is not" {
		if x.left.op != "column" && (x.left.op != "binding" || !a.nullable) {
			return fail("is null requires a column or nullable binding")
		}
		return nil
	}
	if x.op == "in" {
		if !a.column && !a.binding && x.left.op != "lower" || a.typ != 0 && a.typ != TypeText && a.typ != TypeNumber {
			return fail("in needs a column, text or number on the left")
		}
		var typ Type
		if x.right.op == "list" {
			typ = TypeNumber
			if x.right.items[0].value.Kind == KindText {
				typ = TypeText
			}
		} else {
			b, err := v.operand(x.right)
			if err != nil {
				return err
			}
			if x.right.op != "binding" || b.typ != TypeTextList {
				return fail("in requires a literal list or text-list binding")
			}
			typ = TypeText
		}
		if a.typ != 0 && a.typ != typ {
			return fail("in operands have different types")
		}
		return nil
	}
	b, err := v.operand(x.right)
	if err != nil {
		return err
	}
	if x.op == "contains" {
		if !a.column && !a.binding && x.left.op != "lower" || a.typ != 0 && a.typ != TypeText || b.typ != TypeText || b.column || x.right.op == "lower" {
			return fail("contains requires text on the left and a text literal or binding on the right")
		}
		return nil
	}
	for _, pair := range [][2]*Expr{{x.left, x.right}, {x.right, x.left}} {
		lower, other := pair[0], pair[1]
		if lower.op == "lower" && ((lower.left.op != "column" && lower.left.op != "binding") || (other.op != "literal" && other.op != "binding")) {
			return fail("compare lower(column or text binding) with a text literal or binding")
		}
	}
	if a.column && b.column {
		return fail("two columns cannot be compared")
	}
	if !a.column && !a.binding && !b.column && !b.binding {
		return fail("compare a column or a binding")
	}
	if a.typ == TypeTextList || b.typ == TypeTextList {
		return fail("use in for a list binding")
	}
	if a.typ != 0 && b.typ != 0 && a.typ != b.typ {
		return fail("comparison operands have different types")
	}
	return nil
}

func ungroup(x *Expr) *Expr {
	for x.op == "group" {
		x = x.left
	}
	return x
}

// guarded is intentionally a structural check, not a boolean simplifier.
func guarded(x *Expr) bool {
	x = ungroup(x)
	if x.op == "||" {
		return guarded(x.left) && guarded(x.right)
	}
	return hasGuard(x)
}
func hasGuard(x *Expr) bool {
	x = ungroup(x)
	if x.op == "&&" {
		return hasGuard(x.left) || hasGuard(x.right)
	}
	if x.op == "binding" && x.name == "auth.signed_in" {
		return true
	}
	if x.op != "==" {
		return false
	}
	a, b := x.left, x.right
	for i := 0; i < 2; i++ {
		if a.op == "binding" {
			switch a.name {
			case "auth.signed_in":
				if b.op == "literal" && b.value.Kind == KindBool && b.value.Bool {
					return true
				}
			case "auth.id":
				if b.op == "column" && strings.EqualFold(b.name, "visitor_id") {
					return true
				}
			case "auth.role":
				if b.op == "literal" && b.value.Kind == KindText && b.value.Text == "owner" {
					return true
				}
			}
		}
		a, b = b, a
	}
	return false
}
