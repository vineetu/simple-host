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

func (l *lexer) next() (token, *RuleError) {
	for l.pos < len(l.text) && strings.ContainsRune(" \t\r\n", rune(l.text[l.pos])) {
		l.pos++
	}
	start := l.pos
	t := token{pos: start}
	if start == len(l.text) {
		t.kind = "eof"
		return t, nil
	}
	c := l.text[l.pos]
	if identStart(c) {
		l.pos++
		for l.pos < len(l.text) && (identStart(l.text[l.pos]) || digit(l.text[l.pos])) {
			l.pos++
		}
		t.text = l.text[start:l.pos]
		if len(t.text) > 64 {
			return t, refusal("size", start, "identifier exceeds 64 bytes")
		}
		t.kind = "ident"
		if keyword(t.text) {
			t.kind = t.text
		}
		return t, nil
	}
	if c == '\'' || c == '"' {
		s, err := l.stringValue(c)
		t.kind, t.value = "literal", Text(s)
		return t, err
	}
	if digit(c) || c == '-' {
		if c == '-' {
			l.pos++
		}
		if l.pos == len(l.text) || !digit(l.text[l.pos]) {
			return t, refusal("using", start, "expected a JSON number")
		}
		if l.text[l.pos] == '0' {
			l.pos++
		} else {
			for l.pos < len(l.text) && digit(l.text[l.pos]) {
				l.pos++
			}
		}
		integer := true
		if l.pos < len(l.text) && l.text[l.pos] == '.' {
			integer = false
			l.pos++
			if l.pos == len(l.text) || !digit(l.text[l.pos]) {
				return t, refusal("using", l.pos, "expected fraction digits")
			}
			for l.pos < len(l.text) && digit(l.text[l.pos]) {
				l.pos++
			}
		}
		if l.pos < len(l.text) && (l.text[l.pos] == 'e' || l.text[l.pos] == 'E') {
			integer = false
			l.pos++
			if l.pos < len(l.text) && (l.text[l.pos] == '+' || l.text[l.pos] == '-') {
				l.pos++
			}
			if l.pos == len(l.text) || !digit(l.text[l.pos]) {
				return t, refusal("using", l.pos, "expected exponent digits")
			}
			for l.pos < len(l.text) && digit(l.text[l.pos]) {
				l.pos++
			}
		}
		t.kind, t.text = "literal", l.text[start:l.pos]
		if integer {
			if i, err := strconv.ParseInt(t.text, 10, 64); err == nil {
				t.value = Int(i)
				return t, nil
			}
		}
		f, err := strconv.ParseFloat(t.text, 64)
		if err != nil || math.IsInf(f, 0) {
			return t, refusal("type", start, "number must be finite")
		}
		t.value = Float(f)
		return t, nil
	}
	l.pos++
	if strings.ContainsRune("()[],.", rune(c)) {
		t.kind = string(c)
		return t, nil
	}
	if l.pos < len(l.text) {
		pair := l.text[start : l.pos+1]
		switch pair {
		case "||", "&&", "==", "!=":
			l.pos++
			t.kind = pair
			return t, nil
		}
	}
	if c == '!' {
		t.kind = "!"
		return t, nil
	}
	msg := "unsupported token"
	switch c {
	case '=':
		msg = "use == for equality"
	case '|':
		msg = "use || for OR"
	case '&':
		msg = "use && for AND"
	case '<', '>':
		msg = "ordering operators are not supported; use ==, != or in"
	}
	return t, refusal("using", start, msg)
}

func (l *lexer) stringValue(quote byte) (string, *RuleError) {
	start := l.pos
	l.pos++
	var b strings.Builder
	for l.pos < len(l.text) {
		pos := l.pos
		c := l.text[l.pos]
		l.pos++
		if c == quote {
			s := b.String()
			if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
				return "", refusal("using", start, "string must be UTF-8 without NUL")
			}
			return s, nil
		}
		if c == '\n' || c == '\r' {
			return "", refusal("using", pos, "raw newline in string")
		}
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if l.pos == len(l.text) {
			break
		}
		e := l.text[l.pos]
		l.pos++
		switch e {
		case '"', '\'', '\\', '/':
			b.WriteByte(e)
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'u':
			r, ok := l.hexRune()
			if !ok {
				return "", refusal("using", pos, "invalid Unicode escape")
			}
			if utf16.IsSurrogate(r) {
				if r < 0xD800 || r > 0xDBFF || !strings.HasPrefix(l.text[l.pos:], `\u`) {
					return "", refusal("using", pos, "unpaired Unicode surrogate")
				}
				l.pos += 2
				low, ok := l.hexRune()
				if !ok || low < 0xDC00 || low > 0xDFFF {
					return "", refusal("using", pos, "unpaired Unicode surrogate")
				}
				r = utf16.DecodeRune(r, low)
			}
			b.WriteRune(r)
		default:
			return "", refusal("using", pos, "unsupported string escape")
		}
	}
	return "", refusal("using", start, "unterminated string")
}

func (l *lexer) hexRune() (rune, bool) {
	if len(l.text)-l.pos < 4 {
		return 0, false
	}
	n, err := strconv.ParseUint(l.text[l.pos:l.pos+4], 16, 16)
	l.pos += 4
	return rune(n), err == nil
}
