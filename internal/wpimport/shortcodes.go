package wpimport

import (
	"regexp"
	"strings"
)

type shortcode struct {
	Tag   string
	Attrs map[string]any
	Body  string
}

var shortOpen = regexp.MustCompile(`^\[([a-z0-9_-]+)(\s[^\]]*|/?)\]`)
var shortAttrs = regexp.MustCompile(`([a-zA-Z0-9_-]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s\]]+))|"([^"]*)"|'([^']*)'|([^\s]+)`)

func parseAttrs(s string) map[string]any {
	a := map[string]any{}
	n := 0
	for _, m := range shortAttrs.FindAllStringSubmatch(strings.TrimSpace(strings.TrimSuffix(s, "/")), -1) {
		if m[1] != "" {
			v := m[2]
			if m[3] != "" {
				v = m[3]
			}
			if m[4] != "" {
				v = m[4]
			}
			a[strings.ToLower(m[1])] = v
		} else {
			v := m[5]
			if m[6] != "" {
				v = m[6]
			}
			if m[7] != "" {
				v = m[7]
			}
			a[jsonNumber(float64(n))] = v
			n++
		}
	}
	return a
}
func renderShortcodes(s string, render func(shortcode) string) string {
	var out strings.Builder
	for p := 0; p < len(s); {
		if s[p] != '[' {
			out.WriteByte(s[p])
			p++
			continue
		}
		if strings.HasPrefix(s[p:], "[[") {
			if e := strings.Index(s[p+2:], "]]"); e >= 0 {
				out.WriteString(s[p+1 : p+2+e+1])
				p += e + 4
				continue
			}
		}
		m := shortOpen.FindStringSubmatch(s[p:])
		if m == nil {
			out.WriteByte(s[p])
			p++
			continue
		}
		end := p + len(m[0])
		body := ""
		if !strings.HasSuffix(strings.TrimSpace(m[2]), "/") {
			close := "[/" + m[1] + "]"
			cursor := end
			depth := 1
			for cursor < len(s) {
				nextClose := strings.Index(s[cursor:], close)
				if nextClose < 0 {
					break
				}
				ci := cursor + nextClose
				nextOpen := strings.Index(s[cursor:ci], "["+m[1])
				if nextOpen >= 0 {
					oi := cursor + nextOpen
					if nested := shortOpen.FindStringSubmatch(s[oi:]); nested != nil && nested[1] == m[1] && !strings.HasSuffix(strings.TrimSpace(nested[2]), "/") {
						depth++
						cursor = oi + len(nested[0])
						continue
					}
				}
				depth--
				if depth == 0 {
					body = s[end:ci]
					end = ci + len(close)
					break
				}
				cursor = ci + len(close)
			}
		}
		out.WriteString(render(shortcode{m[1], parseAttrs(m[2]), body}))
		p = end
	}
	return out.String()
}
