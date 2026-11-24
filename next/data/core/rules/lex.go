package rules

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type token struct {
	kind, text string
	pos        int
	value      Value
}
type lexer struct {
	text string
	pos  int
}

func identStart(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_' }
func digit(b byte) bool      { return b >= '0' && b <= '9' }
func keyword(s string) bool {
	switch s {
	case "true", "false", "null", "in", "contains", "is", "not", "lower", "row":
		return true
	}
	return false
}

// symbols are the operator and punctuation tokens; a token's kind is its text.
var symbols = map[string]bool{
	"||": true, "&&": true, "==": true, "!=": true,
	"(": true, ")": true, "[": true, "]": true, ",": true, ".": true, "!": true,
}

// misusedSymbols explain the near-miss characters people type from other languages.
var misusedSymbols = map[byte]string{
	'=': "use == for equality",
	'|': "use || for OR",
	'&': "use && for AND",
	'<': "ordering operators are not supported; use ==, != or in",
	'>': "ordering operators are not supported; use ==, != or in",
}

// simpleEscapes maps a JSON string escape letter to the byte it stands for.
var simpleEscapes = map[byte]byte{
	'"': '"', '\'': '\'', '\\': '\\', '/': '/',
	'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t',
}

func (l *lexer) next() (token, *RuleError) {
	for l.pos < len(l.text) && strings.ContainsRune(" \t\r\n", rune(l.text[l.pos])) {
		l.pos++
	}
	if l.pos == len(l.text) {
		return token{kind: "eof", pos: l.pos}, nil
	}
	switch c := l.text[l.pos]; {
	case identStart(c):
		return l.ident()
	case c == '\'' || c == '"':
		t := token{kind: "literal", pos: l.pos}
		s, err := l.stringValue(c)
		t.value = Text(s)
		return t, err
	case digit(c) || c == '-':
		return l.number()
	}
	return l.symbol()
}

func (l *lexer) ident() (token, *RuleError) {
	t := token{pos: l.pos}
	for l.pos < len(l.text) && (identStart(l.text[l.pos]) || digit(l.text[l.pos])) {
		l.pos++
	}
	t.text = l.text[t.pos:l.pos]
	if len(t.text) > 64 {
		return t, refusal("size", t.pos, "identifier exceeds 64 bytes")
	}
	t.kind = "ident"
	if keyword(t.text) {
		t.kind = t.text
	}
	return t, nil
}

func (l *lexer) symbol() (token, *RuleError) {
	t := token{pos: l.pos}
	for _, n := range []int{2, 1} {
		if s := l.text[l.pos:min(l.pos+n, len(l.text))]; len(s) == n && symbols[s] {
			l.pos += n
			t.kind = s
			return t, nil
		}
	}
	msg, ok := misusedSymbols[l.text[l.pos]]
	if !ok {
		msg = "unsupported token"
	}
	l.pos++
	return t, refusal("using", t.pos, msg)
}

// number lexes a JSON number; integers that fit become Int, the rest Float.
func (l *lexer) number() (token, *RuleError) {
	t := token{pos: l.pos}
	l.accept("-")
	if l.pos == len(l.text) || !digit(l.text[l.pos]) {
		return t, refusal("using", t.pos, "expected a JSON number")
	}
	if !l.accept("0") {
		l.digits()
	}
	integer := true
	if l.accept(".") {
		integer = false
		if l.digits() == 0 {
			return t, refusal("using", l.pos, "expected fraction digits")
		}
	}
	if l.accept("eE") {
		integer = false
		l.accept("+-")
		if l.digits() == 0 {
			return t, refusal("using", l.pos, "expected exponent digits")
		}
	}
	t.kind, t.text = "literal", l.text[t.pos:l.pos]
	if i, err := strconv.ParseInt(t.text, 10, 64); integer && err == nil {
		t.value = Int(i)
		return t, nil
	}
	f, err := strconv.ParseFloat(t.text, 64)
	if err != nil || math.IsInf(f, 0) {
		return t, refusal("type", t.pos, "number must be finite")
	}
	t.value = Float(f)
	return t, nil
}

// accept consumes one byte if it is any of chars.
func (l *lexer) accept(chars string) bool {
	if l.pos < len(l.text) && strings.IndexByte(chars, l.text[l.pos]) >= 0 {
		l.pos++
		return true
	}
	return false
}

// digits consumes a run of decimal digits and returns how many there were.
func (l *lexer) digits() int {
	start := l.pos
	for l.pos < len(l.text) && digit(l.text[l.pos]) {
		l.pos++
	}
	return l.pos - start
}

func (l *lexer) stringValue(quote byte) (string, *RuleError) {
	start := l.pos
	l.pos++
	var b strings.Builder
	for l.pos < len(l.text) {
		pos := l.pos
		c := l.text[l.pos]
		l.pos++
		switch {
		case c == quote:
			s := b.String()
			if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
				return "", refusal("using", start, "string must be UTF-8 without NUL")
			}
			return s, nil
		case c == '\n' || c == '\r':
			return "", refusal("using", pos, "raw newline in string")
		case c != '\\':
			b.WriteByte(c)
		case l.pos < len(l.text):
			if err := l.escape(&b, pos); err != nil {
				return "", err
			}
		}
	}
	return "", refusal("using", start, "unterminated string")
}

// escape decodes the escape after a backslash at pos into b.
func (l *lexer) escape(b *strings.Builder, pos int) *RuleError {
	e := l.text[l.pos]
	l.pos++
	if r, ok := simpleEscapes[e]; ok {
		b.WriteByte(r)
		return nil
	}
	if e != 'u' {
		return refusal("using", pos, "unsupported string escape")
	}
	r, ok := l.hexRune()
	if !ok {
		return refusal("using", pos, "invalid Unicode escape")
	}
	if utf16.IsSurrogate(r) {
		if r > 0xDBFF || !strings.HasPrefix(l.text[l.pos:], `\u`) {
			return refusal("using", pos, "unpaired Unicode surrogate")
		}
		l.pos += 2
		low, ok := l.hexRune()
		if !ok || low < 0xDC00 || low > 0xDFFF {
			return refusal("using", pos, "unpaired Unicode surrogate")
		}
		r = utf16.DecodeRune(r, low)
	}
	b.WriteRune(r)
	return nil
}

func (l *lexer) hexRune() (rune, bool) {
	if len(l.text)-l.pos < 4 {
		return 0, false
	}
	n, err := strconv.ParseUint(l.text[l.pos:l.pos+4], 16, 16)
	l.pos += 4
	return rune(n), err == nil
}
