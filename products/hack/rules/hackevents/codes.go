// Package hackevents holds the pure Hack event rules: deterministic text and
// code decisions with no SQL, HTTP, filesystem or provider calls, and no
// ambient clock (docs/architecture/target.md §2). Moved out of
// internal/handler/hack*.go in cleanup 3.2 slice 1c.
package hackevents

import (
	"crypto/rand"
	"strings"
)

// codeAlphabet avoids visually confusable characters (no 0/o/1/i/l): a join,
// judge or team code read aloud or typed by hand should not need a second
// guess.
const codeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// RandomCode returns n random characters from codeAlphabet, rejection
// sampling each byte so every character is equally likely. Was hack.go's
// randomHackCode.
func RandomCode(n int) (string, error) {
	b := make([]byte, n)
	max := 256 - (256 % len(codeAlphabet))
	for i := 0; i < n; i++ {
		var buf [1]byte
		for {
			if _, err := rand.Read(buf[:]); err != nil {
				return "", err
			}
			if int(buf[0]) < max {
				b[i] = codeAlphabet[int(buf[0])%len(codeAlphabet)]
				break
			}
		}
	}
	return string(b), nil
}

// NormalizeCode lowercases a join/judge/team code and drops spaces and
// hyphens, so "AB12 CD34" and "ab12-cd34" are the same code. Was hack.go's
// normalizeHackCode.
func NormalizeCode(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == ' ' || r == '-' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
