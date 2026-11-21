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
		add(invalidUTF8Line(data), "use valid UTF-8 text")
		return nil, problems
	}
	rs := &Rules{}
	for i, line := range strings.Split(string(data), "\n") {
		n := i + 1
		if len(line) > 2048 {
			add(n, "line exceeds 2048 bytes; shorten this rule")
			continue
		}
		tokens := ruleTokens(line)
		if len(tokens) == 0 {
			continue
		}
		if len(rs.rules) >= opt.MaxRules {
			add(n, fmt.Sprintf("too many rules; maximum is %d", opt.MaxRules))
			continue
		}
		r, problem := parseRule(tokens, n)
		if problem != "" {
			add(n, problem)
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
		if problem := rs.chainProblem(r, exists); problem != "" {
			add(r.line, problem)
		}
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return rs, nil
}

// invalidUTF8Line is the 1-based line holding the first invalid byte.
func invalidUTF8Line(data []byte) int {
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
	return line
}

// ruleTokens splits a line into fields, dropping everything from the first
// field that starts with #.
func ruleTokens(line string) []string {
	tokens := strings.Fields(line)
	for j, t := range tokens {
		if strings.HasPrefix(t, "#") {
			return tokens[:j]
		}
	}
	return tokens
}

var statusCodes = map[int]bool{301: true, 302: true, 404: true, 410: true}

// parseRule reads FROM [key=value ...] TO [status[!]] from one line's tokens,
// or says what is wrong with it.
func parseRule(tokens []string, n int) (rule, string) {
	if len(tokens) < 2 {
		return rule{}, "write FROM [key=value ...] TO [301|302|404|410[!]]"
	}
	r := rule{from: tokens[0], status: 301, line: n}
	end := len(tokens) - 1
	last := tokens[end]
	if !strings.HasPrefix(last, "/") && !strings.HasPrefix(last, "http://") && !strings.HasPrefix(last, "https://") && end >= 2 {
		code, err := strconv.Atoi(strings.TrimSuffix(last, "!"))
		if err != nil || !statusCodes[code] {
			return rule{}, "supported status codes are 301, 302, 404 and 410, optionally followed by !"
		}
		r.status = code
		r.force = strings.HasSuffix(last, "!")
		end--
	}
	r.to = tokens[end]
	from, err := url.PathUnescape(r.from)
	if err != nil || !strings.HasPrefix(from, "/") || strings.HasPrefix(from, "//") || strings.ContainsAny(from, "\\?") || strings.ContainsFunc(from, unicode.IsControl) {
		return rule{}, "FROM must be a path on this site, such as /old; put query conditions in separate key=value tokens"
	}
	r.from = normal(from)
	r.segments = strings.Split(strings.TrimPrefix(r.from, "/"), "/")
	conditions, defined, ok := patternCaptures(r.segments, tokens[1:end])
	if !ok {
		return rule{}, "use literal segments, :name segments, and a final *; no .. segments; query conditions must be key=value"
	}
	r.conditions = conditions
	return r, targetProblem(r.to, r.status, defined)
}

// patternCaptures checks FROM's segments and the key=value condition tokens,
// returning the conditions and the placeholder names they define.
func patternCaptures(segments, tokens []string) ([]condition, map[string]bool, bool) {
	var conditions []condition
	defined := map[string]bool{}
	bad := false
	for j, s := range segments {
		if s == ".." || strings.Contains(s, "*") && (s != "*" || j != len(segments)-1) {
			bad = true
		}
		if strings.HasPrefix(s, ":") {
			bad = bad || !identifier.MatchString(s[1:])
			defined[s[1:]] = true
		}
		if s == "*" {
			defined["splat"] = true
		}
	}
	for _, t := range tokens {
		k, v, ok := strings.Cut(t, "=")
		if !ok || k == "" || strings.ContainsAny(k, ":?&") {
			bad = true
			continue
		}
		if strings.HasPrefix(v, ":") {
			bad = bad || !identifier.MatchString(v[1:])
			defined[v[1:]] = true
		}
		conditions = append(conditions, condition{k, v})
	}
	return conditions, defined, !bad
}

// targetProblem says what is wrong with a rule's TO, given the placeholders
// its FROM and query conditions define; "" means it is fine.
func targetProblem(to string, status int, defined map[string]bool) string {
	u, err := url.Parse(to)
	if err != nil || unsafe(to) || strings.Contains(to, "\\") || strings.HasPrefix(to, "//") || strings.Contains(to, "#") {
		return "TO must be a local /path or an absolute http:// or https:// URL without spaces, controls, backslashes or fragments"
	}
	external := u.IsAbs()
	bad := false
	if external {
		bad = u.Scheme != "http" && u.Scheme != "https" || u.Host == "" || u.User != nil || reference.MatchString(u.Host)
	} else {
		bad = !strings.HasPrefix(to, "/")
	}
	for _, seg := range strings.Split(u.Path, "/") {
		bad = bad || seg == "." || seg == ".."
	}
	if bad {
		return "TO must be /path or http(s)://literal-host/path; placeholders are allowed only in path and query"
	}
	if external && (status == 404 || status == 410) {
		return "404 and 410 need a local target page, such as /gone.html"
	}
	// Inspect path/query only: a literal port is not a capture.
	for _, ref := range reference.FindAllStringSubmatch(u.EscapedPath()+"?"+u.RawQuery, -1) {
		if !defined[ref[1]] {
			return "TO uses an undefined placeholder; define it in FROM or a query condition (:splat needs a final *)"
		}
	}
	return ""
}

// chainProblem follows the redirects a request matching r would go through
// and says whether they loop or run past MaxChain; "" means they end.
func (rs *Rules) chainProblem(r rule, exists func(string) bool) string {
	if r.status != 301 && r.status != 302 {
		return ""
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
			return "redirect loop involving lines " + strings.Join(involved, ", ") + "; change a target"
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
			return ""
		}
		u, _ := url.Parse(m.Location("", q.Encode()))
		if u == nil || u.IsAbs() {
			return ""
		}
		if hop == MaxChain {
			return fmt.Sprintf("more than %d redirects in a row from here (lines %s), which browsers give up on or never finish; point the old address straight at its final page", MaxChain, strings.Join(involved, ", "))
		}
		sample, q = u.Path, u.Query()
	}
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
