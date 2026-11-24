package sites

import (
	"fmt"
	"strings"
)

// Redirect is one line of a site's _redirects file: "old-path new-path [301|302]".
type Redirect struct {
	From, To string
	Status   int
}

// ParseRedirects reads a _redirects file. Each mistake is reported as
// "line N: what is wrong"; good lines are returned either way.
func ParseRedirects(text string) ([]Redirect, []string) {
	var out []Redirect
	var mistakes []string
	for i, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		bad := func(why string) { mistakes = append(mistakes, fmt.Sprintf("line %d: %s", i+1, why)) }
		rd := Redirect{Status: 301}
		switch {
		case len(f) < 2 || len(f) > 3:
			bad("write it as: /old-path /new-path [301 or 302]")
			continue
		case len(f) == 3 && f[2] == "302":
			rd.Status = 302
		case len(f) == 3 && f[2] != "301":
			bad("the status must be 301 or 302")
			continue
		}
		rd.From, rd.To = f[0], f[1]
		switch {
		case !strings.HasPrefix(rd.From, "/"):
			bad("the old path must start with /")
		case strings.ContainsAny(rd.From, "*:?"):
			bad("splats, placeholders and query rules are not supported; list each path")
		case !strings.HasPrefix(rd.To, "/") && !strings.HasPrefix(rd.To, "https://") && !strings.HasPrefix(rd.To, "http://"):
			bad("the new path must start with / or https://")
		default:
			out = append(out, rd)
		}
	}
	return out, mistakes
}
