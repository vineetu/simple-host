// Package redirects parses the supported, path-based _redirects format without I/O.
package redirects

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxFileBytes = 128 << 10

// MaxChain is the most redirects one request may follow through a file's
// own rules before it reaches a page or another site.
const MaxChain = 10

// sampleValue stands in for captured values when following a file's own
// chains on publish. It can never be a file name, so a page that happens to
// be called "sample" cannot hide a loop.
const sampleValue = "\x00"

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var reference = regexp.MustCompile(`:([A-Za-z_][A-Za-z0-9_]*)`)

type Problems []string

func (p Problems) Error() string { return strings.Join(p, "\n") }

type condition struct{ key, value string }
type rule struct {
	from, to     string
	segments     []string
	conditions   []condition
	status, line int
	force        bool
}
type Rules struct{ rules []rule }
type Match struct {
	Status  int
	Force   bool
	target  string
	values  map[string]string
	forward bool
}

func unsafe(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) })
}
func normal(s string) string {
	if s == "/" {
		return s
	}
	return strings.TrimRight(s, "/")
}

// Options tune Parse. Exists reports whether a decoded site path resolves to
// content (a file, path.html or a directory) in the version being published:
// a non-forced rule is shadowed there, so a chain ends at such a path.
// SkipLoopCheck is for serving a file that was already validated on publish.
type Options struct {
	MaxRules      int
	Exists        func(string) bool
	SkipLoopCheck bool
}

// Parse validates data with no knowledge of the site's files (every target
// is assumed missing, the strictest loop check).
func Parse(data []byte, maxRules int) (*Rules, error) {
	return ParseWith(data, Options{MaxRules: maxRules})
}

// ParseWith is Parse with Options.
func ParseWith(data []byte, opt Options) (*Rules, error) {
	maxRules := opt.MaxRules
	exists := opt.Exists
	if exists == nil {
		exists = func(string) bool { return false }
	}
	var problems Problems
	add := func(n int, s string) {
		if len(problems) < 20 {
			problems = append(problems, fmt.Sprintf("line %d: %s", n, s))
		}
	}
	if len(data) > MaxFileBytes {
		add(1, "file exceeds 128 KiB; split or remove rules")
		return nil, problems
	}
	if !utf8.Valid(data) {
		line := 1
		for len(data) > 0 {
			r, n := utf8.DecodeRune(data)
			if r == utf8.RuneError && n == 1 {
				break
			}
			if r == '\n' {
				line++
			}
			data = data[n:]
		}
		add(line, "use valid UTF-8 text")
		return nil, problems
	}
	rs := &Rules{}
	for i, line := range strings.Split(string(data), "\n") {
		n := i + 1
		if len(line) > 2048 {
			add(n, "line exceeds 2048 bytes; shorten this rule")
			continue
		}
		tokens := strings.Fields(line)
		for j, t := range tokens {
			if strings.HasPrefix(t, "#") {
				tokens = tokens[:j]
				break
			}
		}
		if len(tokens) == 0 {
			continue
		}
		if len(rs.rules) >= maxRules {
			add(n, fmt.Sprintf("too many rules; maximum is %d", maxRules))
			continue
		}
		if len(tokens) < 2 {
			add(n, "write FROM [key=value ...] TO [301|302|404|410[!]]")
			continue
		}
		r := rule{from: tokens[0], status: 301, line: n}
		end := len(tokens) - 1
		last := tokens[end]
		if !strings.HasPrefix(last, "/") && !strings.HasPrefix(last, "http://") && !strings.HasPrefix(last, "https://") && end >= 2 {
			code, err := strconv.Atoi(strings.TrimSuffix(last, "!"))
			if err != nil || (code != 301 && code != 302 && code != 404 && code != 410) {
				add(n, "supported status codes are 301, 302, 404 and 410, optionally followed by !")
				continue
			}
			r.status = code
			r.force = strings.HasSuffix(last, "!")
			end--
		}
		r.to = tokens[end]
		from, err := url.PathUnescape(r.from)
		if err != nil || !strings.HasPrefix(from, "/") || strings.HasPrefix(from, "//") || strings.ContainsAny(from, "\\?") || strings.ContainsFunc(from, unicode.IsControl) {
			add(n, "FROM must be a path on this site, such as /old; put query conditions in separate key=value tokens")
			continue
		}
		r.from = normal(from)
		r.segments = strings.Split(strings.TrimPrefix(r.from, "/"), "/")
		defined := map[string]bool{}
		bad := false
		for j, s := range r.segments {
			if s == ".." || strings.Contains(s, "*") && (s != "*" || j != len(r.segments)-1) {
				bad = true
			}
			if strings.HasPrefix(s, ":") {
				if !identifier.MatchString(s[1:]) {
					bad = true
				}
				defined[s[1:]] = true
			}
			if s == "*" {
				defined["splat"] = true
			}
		}
		for _, t := range tokens[1:end] {
			k, v, ok := strings.Cut(t, "=")
			if !ok || k == "" || strings.ContainsAny(k, ":?&") {
				bad = true
				continue
			}
			if strings.HasPrefix(v, ":") {
				if !identifier.MatchString(v[1:]) {
					bad = true
				}
				defined[v[1:]] = true
			}
			r.conditions = append(r.conditions, condition{k, v})
		}
		if bad {
			add(n, "use literal segments, :name segments, and a final *; no .. segments; query conditions must be key=value")
			continue
		}
		u, err := url.Parse(r.to)
		if err != nil || unsafe(r.to) || strings.Contains(r.to, "\\") || strings.HasPrefix(r.to, "//") || strings.Contains(r.to, "#") {
			add(n, "TO must be a local /path or an absolute http:// or https:// URL without spaces, controls, backslashes or fragments")
			continue
		}
		external := u.IsAbs()
		if external {
			if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
				bad = true
			}
			if len(reference.FindAllString(u.Host, -1)) > 0 {
				bad = true
			}
		} else if !strings.HasPrefix(r.to, "/") {
			bad = true
		}
		for _, seg := range strings.Split(u.Path, "/") {
			if seg == "." || seg == ".." {
				bad = true
			}
		}
		if bad {
			add(n, "TO must be /path or http(s)://literal-host/path; placeholders are allowed only in path and query")
			continue
		}
		if external && (r.status == 404 || r.status == 410) {
			add(n, "404 and 410 need a local target page, such as /gone.html")
			continue
		}
		// Inspect path/query only: a literal port is not a capture.
		for _, ref := range reference.FindAllStringSubmatch(u.EscapedPath()+"?"+u.RawQuery, -1) {
			if !defined[ref[1]] {
				bad = true
			}
		}
		if bad {
			add(n, "TO uses an undefined placeholder; define it in FROM or a query condition (:splat needs a final *)")
			continue
		}
		rs.rules = append(rs.rules, r)
	}
	if len(problems) > 0 {
		return nil, problems
	}
	if opt.SkipLoopCheck {
		return rs, nil
	}
	for _, r := range rs.rules {
		if r.status != 301 && r.status != 302 {
			continue
		}
		sample := r.from
		for _, s := range r.segments {
			if strings.HasPrefix(s, ":") {
				sample = strings.ReplaceAll(sample, s, sampleValue)
			}
		}
		sample = strings.ReplaceAll(sample, "*", sampleValue)
		q := url.Values{}
		for _, c := range r.conditions {
			v := c.value
			if strings.HasPrefix(v, ":") {
				v = sampleValue
			}
			q.Set(c.key, v)
		}
		seen := map[string]bool{}
		involved := []string{}
		// Follow the rules as a visitor's browser would (first match wins,
		// a page of the site shadows a non-forced rule). A revisit is a loop;
		// a chain that has not ended after MaxChain redirects (a growing
		// splat, a long cycle) is refused too, so a published file always ends.
		for hop := 0; ; hop++ {
			key := normal(sample) + "?" + q.Encode()
			if seen[key] {
				add(r.line, "redirect loop involving lines "+strings.Join(involved, ", ")+"; change a target")
				break
			}
			seen[key] = true
			var m Match
			found := false
			for _, next := range rs.rules {
				if m, found = match(next, sample, q); found {
					involved = append(involved, strconv.Itoa(next.line))
					break
				}
			}
			if !found || m.Status != 301 && m.Status != 302 || !m.Force && exists(sample) {
				break
			}
			u, _ := url.Parse(m.Location("", q.Encode()))
			if u == nil || u.IsAbs() {
				break
			}
			if hop == MaxChain {
				add(r.line, fmt.Sprintf("more than %d redirects in a row from here (lines %s), which browsers give up on or never finish; point the old address straight at its final page", MaxChain, strings.Join(involved, ", ")))
				break
			}
			sample, q = u.Path, u.Query()
		}
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return rs, nil
}
func match(r rule, reqPath string, query url.Values) (Match, bool) {
	if r.from == "/" && reqPath != "/" {
		return Match{}, false
	}
	seg := strings.Split(strings.TrimPrefix(normal(reqPath), "/"), "/")
	values := map[string]string{}
	for i, s := range r.segments {
		if s == "*" {
			if i > len(seg) {
				return Match{}, false
			}
			values["splat"] = strings.Join(seg[i:], "/")
			goto conditions
		}
		if i >= len(seg) {
			return Match{}, false
		}
		if strings.HasPrefix(s, ":") {
			if seg[i] == "" {
				return Match{}, false
			}
			values[s[1:]] = seg[i]
		} else if s != seg[i] {
			return Match{}, false
		}
	}
	if len(seg) != len(r.segments) {
		return Match{}, false
	}
conditions:
	for _, c := range r.conditions {
		vs, ok := query[c.key]
		if !ok {
			return Match{}, false
		}
		found := false
		for _, v := range vs {
			if strings.HasPrefix(c.value, ":") {
				values[c.value[1:]] = v
				found = true
				break
			}
			if v == c.value {
				found = true
				break
			}
		}
		if !found {
			return Match{}, false
		}
	}
	return Match{r.status, r.force, r.to, values, len(r.conditions) == 0 && !strings.Contains(r.to, "?")}, true
}
func (r *Rules) Match(p string, q url.Values) (Match, bool) {
	for _, rule := range r.rules {
		if m, ok := match(rule, p, q); ok {
			return m, true
		}
	}
	return Match{}, false
}
func (m Match) Location(prefix, rawQuery string) string {
	u, _ := url.Parse(m.target)
	substitute := func(s string, query bool) string {
		return reference.ReplaceAllStringFunc(s, func(ref string) string {
			v := m.values[ref[1:]]
			if query {
				return url.QueryEscape(v)
			}
			if ref == ":splat" {
				parts := strings.Split(v, "/")
				for i := range parts {
					parts[i] = url.PathEscape(parts[i])
				}
				return strings.Join(parts, "/")
			}
			return url.PathEscape(v)
		})
	}
	p := substitute(u.EscapedPath(), false)
	base := ""
	if u.IsAbs() {
		base = u.Scheme + "://" + u.Host
	} else {
		// Captured "." or ".." segments resolve inside the site, never above
		// its public mount; leading slashes collapse (no //other-host).
		slash := strings.HasSuffix(p, "/")
		p = path.Clean("/" + strings.TrimLeft(p, "/"))
		if slash && p != "/" {
			p += "/"
		}
		base = strings.TrimRight(prefix, "/")
	}
	result := base + p
	if u.RawQuery != "" || u.ForceQuery {
		result += "?" + substitute(u.RawQuery, true)
	} else if m.forward && rawQuery != "" {
		var safe strings.Builder
		for _, r := range rawQuery {
			if unicode.IsControl(r) || unicode.IsSpace(r) {
				safe.WriteString(url.QueryEscape(string(r)))
			} else {
				safe.WriteRune(r)
			}
		}
		result += "?" + safe.String()
	}
	return result
}
