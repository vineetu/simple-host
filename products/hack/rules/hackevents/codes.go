// Package hackevents holds the pure Hack event rules.
package hackevents

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// codeAlphabet avoids visually confusable characters.
const codeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// RandomCode returns n random characters from codeAlphabet, rejection sampling each byte so every character is equally likely.
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

// NormalizeCode lowercases a join/judge/team code and drops spaces and hyphens, so "AB12 CD34" and "ab12-cd34" are the same code.
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

// HashCode is how a join or invitation code is stored: its SHA-256, in hex.
func HashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}
