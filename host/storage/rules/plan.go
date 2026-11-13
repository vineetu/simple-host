package rules

import (
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

// Plan owns its trees and schema. Request evaluation never mutates it.
type Plan struct {
	table        string
	schema       BindingSchema
	lim          Limits
	policies     []plannedPolicy
	using, check [5]*Expr
}
type plannedPolicy struct {
	command      Command
	restrictive  bool
	using, check *Expr
	shapeUsing   *Expr // Before negation rewriting: the `for` check is structural.
}

func applies(command Command, op Op) bool {
	return command == CmdAll || command == map[Op]Command{OpRead: CmdRead, OpAdd: CmdAdd, OpEdit: CmdEdit, OpDelete: CmdDelete}[op]
}
func walk(x *Expr, fn func(*Expr)) {
	if x == nil {
		return
	}
	fn(x)
	walk(x.left, fn)
	walk(x.right, fn)
	for _, item := range x.items {
		walk(item, fn)
	}
}
func (p *Plan) names(op Op, includeRead, permissiveOnly bool) []string {
	names := map[string]bool{}
	for _, pol := range p.policies {
		if permissiveOnly && pol.restrictive {
			continue
		}
		if !applies(pol.command, op) && !(includeRead && (op == OpEdit || op == OpDelete) && applies(pol.command, OpRead)) {
			continue
		}
		for _, x := range []*Expr{pol.using, pol.check} {
			walk(x, func(x *Expr) {
				if x.op == "binding" {
					names[x.name] = true
				}
			})
		}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Needs includes read bindings for operations whose callers also filter by read.
func (p *Plan) Needs(op Op) []string { return p.names(op, true, false) }
func (p *Plan) ReferencesBinding(op Op, name string) bool {
	for _, n := range p.names(op, false, false) {
		if n == name {
			return true
		}
	}
	return false
}
func (p *Plan) NeedsIdentity(op Op) bool { return len(p.names(op, false, true)) != 0 }
func (p *Plan) ReferencesColumn(name string) bool {
	found := false
	for _, pol := range p.policies {
		for _, x := range []*Expr{pol.using, pol.check} {
			walk(x, func(x *Expr) {
				if x.op == "column" && asciiLower(x.name) == asciiLower(name) {
					found = true
				}
			})
		}
	}
	return found
}
func (p *Plan) AuthorTracked() bool {
	return p.ReferencesColumn("visitor_id") || p.Partial(OpAdd, Values{"auth.signed_in": Bool(true), "auth.role": Text("visitor"), "auth.ai": Bool(false), "auth.checked": Bool(true)}) != False
}
func (p *Plan) expression(op Op, check bool) (*Expr, error) {
	if check && (op == OpAdd || op == OpEdit) {
		return p.check[op], nil
	}
	if !check && (op == OpRead || op == OpEdit || op == OpDelete) {
		return p.using[op], nil
	}
	return nil, RuleError{Table: p.table, Clause: "command", Position: -1, Message: "operation does not support this clause"}
}
func validValue(v Value, b Binding, lim Limits) bool {
	if v.Kind == KindNull {
		return b.Nullable && (b.Type == TypeText || b.Type == TypeNumber)
	}
	textOK := func(s string) bool { return utf8.ValidString(s) && !strings.ContainsRune(s, 0) }
	switch b.Type {
	case TypeBool:
		return v.Kind == KindBool
	case TypeText:
		return v.Kind == KindText && textOK(v.Text)
	case TypeNumber:
		return v.Kind == KindInt || v.Kind == KindFloat && !math.IsNaN(v.Float) && !math.IsInf(v.Float, 0)
	case TypeTextList:
		if v.Kind != KindList || len(v.List) > lim.ListItems {
			return false
		}
		for _, s := range v.List {
			if !textOK(s) {
				return false
			}
		}
		return true
	}
	return false
}
func (p *Plan) validateValues(op Op, v Values) error {
	for _, name := range p.Needs(op) {
		value, ok := v[name]
		if !ok || !validValue(value, p.schema[name], p.lim) {
			return RuleError{Table: p.table, Clause: "binding", Position: -1, Message: "missing or invalid binding " + name}
		}
	}
	return nil
}
