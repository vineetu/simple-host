package rules

// Expr is a parsed expression. Its tree is private and bounded by Limits.
type Expr struct {
	op          string
	pos, depth  int
	name        string
	value       Value
	left, right *Expr
	items       []*Expr
}

type parser struct {
	lex            lexer
	tok            token
	lim            Limits
	nodes, nesting int
	err            *RuleError
}

// Parse resolves no names. Syntax errors use clause using; callers validating a
// check clause relabel those errors to check. Type and size errors retain theirs.
func Parse(expr string, lim Limits) (*Expr, *RuleError) {
	if len(expr) > lim.ExprBytes {
		return nil, refusal("size", lim.ExprBytes, "expression exceeds byte limit")
	}
	p := parser{lex: lexer{text: expr}, lim: lim}
	p.advance()
	x := p.or()
	if p.err == nil && p.tok.kind != "eof" {
		p.fail("using", p.tok.pos, "unexpected token; comparisons cannot be chained")
	}
	if p.err != nil {
		return nil, p.err
	}
	return x, nil
}

func (p *parser) advance() {
	if p.err != nil {
		return
	}
	p.tok, p.err = p.lex.next()
}
func (p *parser) fail(clause string, pos int, msg string) {
	if p.err == nil {
		p.err = refusal(clause, pos, msg)
	}
}
func (p *parser) take(kind string) bool {
	if p.err == nil && p.tok.kind == kind {
		p.advance()
		return true
	}
	return false
}
func (p *parser) expect(kind string) {
	if !p.take(kind) {
		p.fail("using", p.tok.pos, "expected "+kind)
	}
}
func (p *parser) node(op string, pos int, left, right *Expr) *Expr {
	x := &Expr{op: op, pos: pos, depth: 1, left: left, right: right}
	for _, child := range []*Expr{left, right} {
		if child != nil && child.depth >= x.depth {
			x.depth = child.depth + 1
		}
	}
	p.nodes++
	if p.nodes > p.lim.ExprNodes {
		p.fail("size", pos, "expression exceeds node limit")
	}
	if x.depth > p.lim.ExprDepth {
		p.fail("size", pos, "expression exceeds depth limit")
	}
	return x
}
func (p *parser) enter(pos int) bool {
	p.nesting++
	if p.nesting >= p.lim.ExprDepth {
		p.fail("size", pos, "expression exceeds depth limit")
	}
	return p.err == nil
}
func (p *parser) or() *Expr {
	x := p.and()
	for p.err == nil && p.tok.kind == "||" {
		pos := p.tok.pos
		p.advance()
		x = p.node("||", pos, x, p.and())
	}
	return x
}
func (p *parser) and() *Expr {
	x := p.not()
	for p.err == nil && p.tok.kind == "&&" {
		pos := p.tok.pos
		p.advance()
		x = p.node("&&", pos, x, p.not())
	}
	return x
}
func (p *parser) not() *Expr {
	if p.err != nil {
		return nil
	}
	pos := p.tok.pos
	if p.take("!") {
		if !p.enter(pos) {
			return nil
		}
		x := p.not()
		p.nesting--
		return p.node("!", pos, x, nil)
	}
	if p.take("(") {
		if !p.enter(pos) {
			return nil
		}
		x := p.or()
		p.expect(")")
		p.nesting--
		return p.node("group", pos, x, nil)
	}
	x := p.operand()
	if p.err != nil {
		return nil
	}
	op, pos := p.tok.kind, p.tok.pos
	switch op {
	case "==", "!=", "contains":
		p.advance()
		return p.node(op, pos, x, p.operand())
	case "in":
		p.advance()
		return p.node(op, pos, x, p.list())
	case "is":
		p.advance()
		if p.take("not") {
			op = "is not"
		}
		p.expect("null")
		return p.node(op, pos, x, nil)
	}
	return x
}
func (p *parser) operand() *Expr {
	if p.err != nil {
		return nil
	}
	t := p.tok
	switch t.kind {
	case "literal", "true", "false", "null":
		p.advance()
		x := p.node("literal", t.pos, nil, nil)
		x.value = t.value
		if t.kind == "true" || t.kind == "false" {
			x.value = Bool(t.kind == "true")
		}
		return x
	case "lower":
		p.advance()
		p.expect("(")
		if !p.enter(t.pos) {
			return nil
		}
		x := p.operand()
		p.expect(")")
		p.nesting--
		return p.node("lower", t.pos, x, nil)
	case "ident", "row":
		p.advance()
		op, name := "column", t.text
		if p.take(".") {
			if p.tok.kind != "ident" && !keyword(p.tok.kind) {
				p.fail("using", p.tok.pos, "expected identifier after dot")
				return nil
			}
			name = p.tok.text
			if t.text != "row" {
				op, name = "binding", t.text+"."+name
			}
			p.advance()
		} else if t.kind == "row" {
			p.fail("using", t.pos, "row must qualify a column")
		}
		x := p.node(op, t.pos, nil, nil)
		x.name = name
		return x
	default:
		p.fail("using", t.pos, "expected an operand")
		return nil
	}
}
func (p *parser) list() *Expr {
	if p.err != nil {
		return nil
	}
	pos := p.tok.pos
	if !p.take("[") {
		return p.operand()
	}
	x := p.node("list", pos, nil, nil)
	if p.tok.kind == "]" {
		p.fail("type", pos, "a list needs at least one item")
		return x
	}
	for p.err == nil {
		item := p.operand()
		if p.err != nil {
			return x
		}
		if item.op != "literal" || item.value.Kind != KindText && item.value.Kind != KindInt && item.value.Kind != KindFloat {
			p.fail("type", item.pos, "list items must be text or numbers")
			return x
		}
		if len(x.items) > 0 && (x.items[0].value.Kind == KindText) != (item.value.Kind == KindText) {
			p.fail("type", item.pos, "a list is all text or all numbers")
			return x
		}
		x.items = append(x.items, item)
		x.depth = 2
		if x.depth > p.lim.ExprDepth {
			p.fail("size", pos, "expression exceeds depth limit")
		}
		if len(x.items) > p.lim.ListItems {
			p.fail("size", item.pos, "list exceeds item limit")
		}
		if !p.take(",") {
			break
		}
	}
	p.expect("]")
	return x
}
