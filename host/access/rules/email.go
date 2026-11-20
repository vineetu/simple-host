package rules

import (
	"regexp"
	"strings"
)

var emailShape = regexp.MustCompile(`^[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}$`)

// NormalEmail is the one form an email address is checked, stored and
// compared in: trimmed, lower case, plain ASCII, at most 254 bytes, no "..".
// No provider-specific folding: only the exact address counts (model.BaseEmail
// is "the same person"). ok is false for anything else.
func NormalEmail(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) > 254 || !emailShape.MatchString(s) || strings.Contains(s, "..") {
		return "", false
	}
	return s, true
}
