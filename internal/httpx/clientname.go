package httpx

import (
	"strings"
	"unicode"
)

// CleanClientName makes a self-declared app name safe and short enough to
// show. It is only ever inserted as text, never as markup.
func CleanClientName(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if r := []rune(out); len(r) > 60 {
		out = string(r[:60])
	}
	if out == "" {
		out = "An app"
	}
	return out
}
