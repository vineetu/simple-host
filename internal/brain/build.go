package brain

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Repo names, as they appear in document paths.
const (
	RepoHost       = "simple-host"
	RepoEnterprise = "simple-host-enterprise"
	RepoConsole    = "chhota-console"
	RepoTravel     = "simple-host-travel"
)

// allowlist is every file the bundle may hold, as glob patterns relative to
// each repo's root. Nothing else is read: no .env, no config, no code.
var allowlist = map[string][]string{
	RepoHost: {
		"INTENT.md", "FEATURES.md", "PARITY.md", "CHANGELOG.md",
		"internal/handler/static/llms.txt", "internal/handler/static/hack-llms.txt",
		"docs/designs/*.md", "docs/platforms/*.md", "docs/advanced/README.md",
		"docs/history/*release*.md",
	},
	RepoEnterprise: {"INTENT.md", "FEATURES.md", "PARITY.md", "CHANGELOG.md"},
	RepoConsole:    {"INTENT.md", "FEATURES.md", "ARCHITECTURE.md", "README.md"},
	RepoTravel: {"INTENT.md", "REQUIREMENTS.md", "CHANGELOG.md",
		"docs/FEATURE-MAP.md", "docs/STATUS.md", "docs/install.md", "docs/upgrade.md",
		"docs/settings.md", "docs/self-hosting.md", "docs/connect-your-ai.md",
		"docs/backups.md", "docs/house-rules.md", "docs/first-quote.md", "docs/data-flow.md"},
}

var sourceLabel = map[string]string{
	RepoConsole: "Chhota Break console, live today",
	RepoTravel:  "Simple Host for Travel, open-source plan, not live",
}

var mustHave = map[string][]string{
	RepoHost:       {"INTENT.md", "FEATURES.md", "PARITY.md", "CHANGELOG.md"},
	RepoEnterprise: {"INTENT.md", "FEATURES.md", "PARITY.md", "CHANGELOG.md"},
	RepoConsole:    {"INTENT.md", "FEATURES.md"},
	RepoTravel:     {"INTENT.md", "REQUIREMENTS.md", "CHANGELOG.md"},
}

// LocalSource is an additional repo read once from a clean git archive of
// one branch, with its own allowlist and a label carried on every doc,
// source, decision and change it produces, so an answer never mixes it
// with a different product.
type LocalSource struct {
	Repo     string // bundle repo key; must have an allowlist + mustHave entry
	RepoDir  string // the real git checkout, read only for commit metadata
	Branch   string // branch archived into FilesDir
	FilesDir string // extracted tree to read files from (no .git here)
}

// Allowed reports whether a bundle path ("<repo>/<file>") is one the
// allowlist admits. The builder never adds anything else; tests use it.
func Allowed(p string) bool {
	repo, rel, ok := strings.Cut(p, "/")
	if !ok || rel == "" || path.Clean(rel) != rel || strings.HasPrefix(rel, "../") || strings.Contains(rel, "\\") {
		return false
	}
	for _, pattern := range allowlist[repo] {
		if m, _ := path.Match(pattern, rel); m {
			return true
		}
	}
	return false
}

// maxDocBytes bounds one source file; the largest today is about 270 KB.
const maxDocBytes = 4 << 20

// Build reads allowlisted documents from two checkouts and optional local archives.
func Build(hostDir, enterpriseDir string, now time.Time, locals ...LocalSource) (*Bundle, error) {
	b := &Bundle{Format: Format, BuiltAt: now.UTC()}
	versions := map[string]string{}
	for _, r := range []struct{ name, dir string }{{RepoHost, hostDir}, {RepoEnterprise, enterpriseDir}} {
		src, err := gitSource(r.name, r.dir)
		if err != nil {
			return nil, err
		}
		b.Sources = append(b.Sources, src)
		docs, err := readDocs(r.name, r.dir)
		if err != nil {
			return nil, err
		}
		b.Docs = append(b.Docs, docs...)
	}
	for _, l := range locals {
		src, err := gitSourceAtRef(l.Repo, l.RepoDir, l.Branch)
		if err != nil {
			return nil, err
		}
		b.Sources = append(b.Sources, src)
		docs, err := readDocs(l.Repo, l.FilesDir)
		if err != nil {
			return nil, err
		}
		b.Docs = append(b.Docs, docs...)
	}
	versions["host"] = gitDescribe(hostDir, "v[0-9]*", false)
	versions["host_head"] = gitDescribe(hostDir, "v[0-9]*", true)
	versions["hack"] = gitDescribe(hostDir, "hack-v*", false)
	versions["enterprise"] = gitDescribe(enterpriseDir, "v[0-9]*", false)
	versions["chart"] = gitDescribe(enterpriseDir, "chart-v*", false)

	for _, d := range b.Docs {
		switch path.Base(d.Path) {
		case "INTENT.md":
			b.Decisions = append(b.Decisions, parseDecisions(d)...)
		case "CHANGELOG.md":
			b.Changes = append(b.Changes, parseChanges(d)...)
		}
	}
	sort.SliceStable(b.Decisions, func(i, j int) bool { return b.Decisions[i].Date > b.Decisions[j].Date })
	sort.SliceStable(b.Changes, func(i, j int) bool { return changeKey(b.Changes[i]) > changeKey(b.Changes[j]) })

	hostParity, entParity := b.doc(RepoHost+"/PARITY.md"), b.doc(RepoEnterprise+"/PARITY.md")
	if hostParity == nil {
		return nil, errors.New("simple-host PARITY.md is missing")
	}
	b.Parity = parseParity(*hostParity)
	if len(b.Parity) == 0 {
		return nil, errors.New("no rows found in PARITY.md's feature table")
	}
	b.ParityCopiesMatch = entParity != nil && docText(*hostParity) == docText(*entParity)
	b.Products = buildProducts(b, versions)
	if len(b.Decisions) == 0 || len(b.Changes) == 0 {
		return nil, errors.New("no decisions or no changes parsed; the source layout changed")
	}
	return b, nil
}

// WriteFile writes the bundle beside dst and renames it into place, so a
// reader sees the old bundle or the new one, never half of one.
func WriteFile(b *Bundle, dst string) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".bundle-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

func (b *Bundle) doc(p string) *Doc {
	for i := range b.Docs {
		if b.Docs[i].Path == p {
			return &b.Docs[i]
		}
	}
	return nil
}

func gitSource(repo, dir string) (Source, error) {
	out, err := exec.Command("git", "-C", dir, "log", "-1", "--format=%h%n%cI").Output()
	if err != nil {
		return Source{}, fmt.Errorf("%s: not a git checkout at %s", repo, dir)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return Source{}, fmt.Errorf("%s: unexpected git log output", repo)
	}
	t, _ := time.Parse(time.RFC3339, lines[1])
	return Source{Repo: repo, Commit: lines[0], CommitTime: t.UTC()}, nil
}

func gitSourceAtRef(repo, dir, ref string) (Source, error) {
	out, err := exec.Command("git", "-C", dir, "log", "-1", ref, "--format=%h%n%cI").Output()
	if err != nil {
		return Source{}, fmt.Errorf("%s: not a git checkout at %s", repo, dir)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return Source{}, fmt.Errorf("%s: unexpected git log output", repo)
	}
	t, _ := time.Parse(time.RFC3339, lines[1])
	return Source{Repo: repo, Label: sourceLabel[repo], Commit: lines[0], CommitTime: t.UTC()}, nil
}

func gitDescribe(dir, match string, long bool) string {
	args := []string{"-C", dir, "describe", "--tags", "--match", match}
	if !long {
		args = append(args, "--abbrev=0")
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func readDocs(repo, dir string) ([]Doc, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		return nil, err
	}
	var docs []Doc
	for _, pattern := range allowlist[repo] {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			return nil, err
		}
		sort.Strings(matches)
		for _, m := range matches {
			rel, err := filepath.Rel(root, m)
			if err != nil {
				return nil, err
			}
			p := repo + "/" + filepath.ToSlash(rel)
			if !Allowed(p) {
				continue
			}
			// A symlink could point anywhere on the box: only regular files,
			// reached through no symlinked directory either.
			info, err := os.Lstat(m)
			if err != nil || !info.Mode().IsRegular() || info.Size() > maxDocBytes {
				continue
			}
			if real, err := filepath.EvalSymlinks(m); err != nil || real != m {
				continue
			}
			raw, err := os.ReadFile(m)
			if err != nil {
				return nil, err
			}
			d := parseDoc(p, repo, redact(string(raw)))
			d.Label = sourceLabel[repo]
			docs = append(docs, d)
		}
	}
	for _, must := range mustHave[repo] {
		found := false
		for _, d := range docs {
			if d.Path == repo+"/"+must {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("%s: %s is missing", repo, must)
		}
	}
	return docs, nil
}

// secretShapes are credential formats that must never reach the bundle, even
// if one were pasted into a document by mistake.
var secretShapes = regexp.MustCompile(`(?:sh(?:at|rt|ac|cs)_[A-Za-z0-9_-]{16,}|\bsh_[A-Za-z0-9]{24,}|\bsk-[A-Za-z0-9_-]{20,}|\bgh[pousr]_[A-Za-z0-9]{20,}|\bAKIA[0-9A-Z]{16}\b|\bxox[abprs]-[A-Za-z0-9-]{10,}|-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----)`)

func redact(s string) string { return secretShapes.ReplaceAllString(s, "[redacted]") }

var headingLine = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)

// parseDoc splits a document at its headings, ignoring "#" lines inside
// fenced code. Text before the first heading is a section with no heading.
func parseDoc(p, repo, text string) Doc {
	d := Doc{Path: p, Repo: repo, Title: path.Base(p)}
	seen := map[string]int{}
	cur := Section{ID: "top", Level: 0}
	var body []string
	inFence := false
	flush := func() {
		cur.Text = strings.TrimSpace(strings.Join(body, "\n"))
		if cur.Text != "" || cur.Heading != "" {
			d.Sections = append(d.Sections, cur)
		}
		body = nil
	}
	isMarkdown := strings.HasSuffix(p, ".md")
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
		}
		if m := headingLine.FindStringSubmatch(line); isMarkdown && m != nil && !inFence {
			flush()
			heading := m[2]
			if d.Title == path.Base(p) && len(m[1]) == 1 {
				d.Title = heading
			}
			id := anchor(heading)
			if n := seen[id]; n > 0 {
				seen[id] = n + 1
				id = fmt.Sprintf("%s-%d", id, n)
			} else {
				seen[id] = 1
			}
			cur = Section{ID: id, Heading: heading, Level: len(m[1])}
			continue
		}
		body = append(body, line)
	}
	flush()
	return d
}

var anchorDrop = regexp.MustCompile(`[^\p{L}\p{N} _-]`)

// anchor is GitHub's heading anchor: lowercase, punctuation dropped, spaces
// to hyphens.
func anchor(heading string) string {
	a := anchorDrop.ReplaceAllString(strings.ToLower(heading), "")
	a = strings.ReplaceAll(strings.TrimSpace(a), " ", "-")
	if a == "" {
		return "section"
	}
	return a
}

func docText(d Doc) string {
	var sb strings.Builder
	for _, s := range d.Sections {
		sb.WriteString(s.Heading)
		sb.WriteString("\n")
		sb.WriteString(s.Text)
		sb.WriteString("\n")
	}
	return sb.String()
}

var (
	isoDate = regexp.MustCompile(`\b(20\d\d-\d\d-\d\d)\b`)
	// A decision opens with a bold lead that carries its date:
	// "- **2026-10-16. Title.** text" or "**2026-10-03 owner decision — x.**".
	decisionLead = regexp.MustCompile(`^(?:[-*]\s+)?\*\*([^*]*?\b20\d\d-\d\d-\d\d\b[^*]*?)\*\*\s*(.*)$`)
	datedOpen    = regexp.MustCompile(`^(?:[-*]\s+)?\*\*\s*20\d\d-\d\d-\d\d`)
	listItem     = regexp.MustCompile(`^[-*]\s+`)
)

// parseDecisions finds every dated decision in an INTENT.md: the items of
// its decisions section, and dated bold paragraphs anywhere else.
func parseDecisions(d Doc) []Decision {
	var out []Decision
	for _, s := range d.Sections {
		var cur *Decision
		var text []string
		end := func() {
			if cur != nil {
				cur.Text = strings.TrimSpace(strings.Join(text, "\n"))
				out = append(out, *cur)
			}
			cur, text = nil, nil
		}
		lines := strings.Split(s.Text, "\n")
		for i := 0; i < len(lines); i++ {
			line := lines[i]
			// A bold lead can wrap onto the next lines; join it up first.
			if datedOpen.MatchString(line) {
				for strings.Count(line, "**")%2 == 1 && i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" {
					i++
					line += " " + strings.TrimSpace(lines[i])
				}
			}
			if m := decisionLead.FindStringSubmatch(line); m != nil {
				end()
				lead := strings.TrimSpace(m[1])
				date := isoDate.FindString(lead)
				title := strings.TrimSpace(strings.Trim(strings.TrimSpace(strings.Replace(lead, date, "", 1)), ".:—- "))
				title = strings.TrimPrefix(title, "owner decision — ")
				title = strings.TrimPrefix(title, "owner decision ")
				if title == "" {
					title = s.Heading
				}
				cur = &Decision{Date: date, Title: title, Repo: d.Repo, Source: d.Path + "#" + s.ID, Product: productOf(d.Repo, lead+" "+m[2])}
				text = []string{m[2]}
				continue
			}
			if cur == nil {
				continue
			}
			// A new top-level list item without a dated lead ends the decision.
			if listItem.MatchString(line) {
				end()
				continue
			}
			text = append(text, line)
		}
		end()
	}
	return out
}

// productOf guesses which product an entry is about, for filtering.
func productOf(repo, text string) string {
	if repo == RepoEnterprise {
		return "enterprise"
	}
	if repo == RepoConsole {
		return "console"
	}
	if repo == RepoTravel {
		return "travel"
	}
	t := strings.ToLower(text)
	switch {
	case strings.Contains(t, "for companies") || strings.Contains(t, "company sign-in"):
		return "hack-companies"
	case strings.Contains(t, "standalone") && strings.Contains(t, "hack"):
		return "selfhost-hack"
	case strings.Contains(t, "simple hack") || strings.Contains(t, "simple-hack") || strings.Contains(t, "hackathon"):
		return "hack"
	case strings.Contains(t, "enterprise"):
		return "enterprise"
	case strings.Contains(t, "small box") || strings.Contains(t, "small-box") || strings.Contains(t, "self-hosted"):
		return "selfhost-host"
	}
	return "host"
}

// parseChanges reads a CHANGELOG.md: each heading with a date (or
// "Unreleased") and its list items.
func parseChanges(d Doc) []Change {
	var out []Change
	newer := "" // the date of the nearest dated entry above
	for _, s := range d.Sections {
		if s.Level != 2 {
			continue
		}
		date := isoDate.FindString(s.Heading)
		unreleased := date == "" && strings.EqualFold(strings.TrimSpace(s.Heading), "unreleased")
		if date == "" && !unreleased {
			continue
		}
		title := strings.TrimSpace(strings.Trim(strings.Replace(s.Heading, date, "", 1), " :—-"))
		if title == "" {
			title = s.Heading
		}
		if unreleased && newer != "" {
			// Only the top Unreleased is pending; one left lower down shipped
			// with the entries around it.
			date, title = newer, "Unreleased (left below "+newer+")"
		}
		if !unreleased {
			newer = date
		}
		var items []string
		var cur []string
		for _, line := range strings.Split(s.Text, "\n") {
			if listItem.MatchString(line) {
				if len(cur) > 0 {
					items = append(items, strings.Join(cur, " "))
				}
				cur = []string{strings.TrimSpace(listItem.ReplaceAllString(line, ""))}
			} else if t := strings.TrimSpace(line); t != "" {
				cur = append(cur, t)
			}
		}
		if len(cur) > 0 {
			items = append(items, strings.Join(cur, " "))
		}
		if len(items) == 0 {
			continue
		}
		out = append(out, Change{Date: date, Title: title, Items: items, Repo: d.Repo,
			Product: productOf(d.Repo, title+" "+strings.Join(items, " ")), Source: d.Path + "#" + s.ID})
	}
	return out
}

func changeKey(c Change) string {
	if c.Date == "" {
		return "9999" // Unreleased sorts first
	}
	return c.Date
}

// parseParity reads the four-column feature table of PARITY.md.
func parseParity(d Doc) []ParityRow {
	var out []ParityRow
	for _, s := range d.Sections {
		if !strings.EqualFold(s.Heading, "Feature table") {
			continue
		}
		for _, line := range strings.Split(s.Text, "\n") {
			cells := tableCells(line)
			if len(cells) != 4 || cells[0] == "Area" || strings.Trim(cells[0], "-: ") == "" {
				continue
			}
			row := ParityRow{Area: cells[0], Hosted: cells[1], Enterprise: cells[2], Status: cells[3], State: parityState(cells[3])}
			row.Date = isoDate.FindString(cells[0])
			out = append(out, row)
		}
	}
	return out
}

func tableCells(line string) []string {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "|") {
		return nil
	}
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	// Pipes inside backticks are rare here; an escaped pipe is kept.
	t = strings.ReplaceAll(t, `\|`, "\x00")
	parts := strings.Split(t, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(strings.ReplaceAll(parts[i], "\x00", "|"))
	}
	return parts
}

func parityState(status string) string {
	s := strings.ToLower(strings.Trim(status, "` "))
	switch {
	case strings.HasPrefix(s, "same"):
		return "same"
	case strings.Contains(s, "gap → hosted") || strings.Contains(s, "gap -> hosted"):
		return "gap_hosted"
	case strings.Contains(s, "gap → enterprise") || strings.Contains(s, "gap -> enterprise"):
		return "gap_enterprise"
	case strings.Contains(s, "different on purpose"):
		return "different_on_purpose"
	case strings.HasPrefix(s, "n/a") || strings.Contains(s, "hosted only") || strings.Contains(s, "hosted-only"):
		return "not_applicable"
	}
	return "other"
}

// productSpec says where each product's identity comes from.
type productSpec struct {
	id, name, fallback           string
	what, who, success, nonGoals [2]string // doc path, section heading
	extra                        []string  // more source paths
}

func buildProducts(b *Bundle, v map[string]string) []Product {
	hostIntent, entIntent := RepoHost+"/INTENT.md", RepoEnterprise+"/INTENT.md"
	hackLLMs := RepoHost + "/internal/handler/static/hack-llms.txt"
	specs := []productSpec{
		{id: "host", name: "Simple Host (simple-host.app)",
			what: [2]string{hostIntent, "What this is, and why it exists"}, who: [2]string{hostIntent, "Who uses it, and in what situation"},
			success: [2]string{hostIntent, "What success looks like"}, nonGoals: [2]string{hostIntent, "Non-goals"}},
		{id: "hack", name: "Simple Hack (simple-hack.app)",
			what: [2]string{hostIntent, "The hackathon product, and the page that sells it"}, who: [2]string{hackLLMs, ""},
			extra: []string{RepoHost + "/docs/designs/simple-hack-platform.md"}},
		{id: "enterprise", name: "Simple Host Enterprise",
			what: [2]string{entIntent, "What this is, and why it exists"}, who: [2]string{entIntent, "Who uses it, and in what situation"},
			success: [2]string{entIntent, "What success looks like"}, nonGoals: [2]string{entIntent, "Non-goals"}},
		{id: "selfhost-host", name: "Simple Host small box (self-hosted)",
			fallback: "The small-box edition of Simple Host that anyone can run on one ~$5 VPS with Docker Compose and Caddy: the same binary as simple-host.app, with the path address model by default.",
			what:     [2]string{RepoHost + "/docs/platforms/README.md", "Where it runs"}, nonGoals: [2]string{hostIntent, "Non-goals"}},
		{id: "selfhost-hack", name: "Simple Hack standalone (self-hosted)",
			what:  [2]string{RepoHost + "/docs/platforms/simple-hack-standalone.md", "Install the full Simple Hack platform"},
			extra: []string{RepoHost + "/docs/platforms/simple-hack-coolify.md", RepoHost + "/docs/platforms/simple-hack-digitalocean.md"}},
		{id: "hack-companies", name: "Simple Hack for companies",
			what:  [2]string{hackLLMs, "Simple Hack for companies"},
			extra: []string{RepoHost + "/docs/history/simple-hack-release-v0.8.10.md"}},
	}
	versions := map[string]string{
		"host":           "simple-host.app runs main at " + commitOf(b, RepoHost) + " (" + v["host_head"] + "); latest small-box release " + v["host"],
		"selfhost-host":  "latest small-box release " + v["host"],
		"hack":           "latest Simple Hack release " + v["hack"] + "; simple-hack.app runs main at " + commitOf(b, RepoHost),
		"selfhost-hack":  "latest standalone release " + v["hack"],
		"hack-companies": "the Simple Hack standalone package, release " + v["hack"],
		"enterprise":     "latest release " + v["enterprise"] + ", Helm chart " + strings.TrimPrefix(v["chart"], "chart-"),
	}
	var out []Product
	for _, s := range specs {
		p := Product{ID: s.id, Name: s.name, Version: versions[s.id], NonGoals: []string{}}
		add := func(src string) {
			for _, have := range p.Sources {
				if have == src {
					return
				}
			}
			p.Sources = append(p.Sources, src)
		}
		get := func(ref [2]string) string {
			if ref[0] == "" {
				return ""
			}
			d := b.doc(ref[0])
			if d == nil {
				return ""
			}
			if !strings.HasSuffix(ref[0], ".md") {
				// llms.txt files: a paragraph starting with the heading text,
				// or the first paragraph.
				text := docText(*d)
				for _, para := range strings.Split(text, "\n\n") {
					para = strings.TrimSpace(para)
					if ref[1] == "" || strings.HasPrefix(para, ref[1]) {
						add(ref[0])
						return para
					}
				}
				return ""
			}
			for _, sec := range d.Sections {
				if strings.EqualFold(sec.Heading, ref[1]) {
					add(ref[0] + "#" + sec.ID)
					return sec.Text
				}
			}
			return ""
		}
		p.What = leadProse(get(s.what))
		if p.What == "" {
			p.What = s.fallback
		}
		if s.fallback != "" && p.What != s.fallback {
			p.What = s.fallback + "\n\n" + p.What
		}
		p.WhoFor = clip(get(s.who), 2500)
		p.Success = clip(get(s.success), 2500)
		if ng := get(s.nonGoals); ng != "" {
			p.NonGoals = bullets(ng)
		}
		for _, e := range s.extra {
			if b.doc(e) != nil {
				add(e)
			}
		}
		if p.Sources == nil {
			p.Sources = []string{}
		}
		out = append(out, p)
	}
	return out
}

func commitOf(b *Bundle, repo string) string {
	for _, s := range b.Sources {
		if s.Repo == repo {
			return s.Commit
		}
	}
	return ""
}

// leadProse is the opening of a section: its paragraphs up to the first
// dated decision, link list or table, at most about 2,500 characters.
func leadProse(text string) string {
	var keep []string
	n := 0
	for _, para := range strings.Split(text, "\n\n") {
		t := strings.TrimSpace(para)
		if t == "" {
			continue
		}
		if decisionLead.MatchString(t) || strings.HasPrefix(t, "|") || strings.HasPrefix(t, "```") {
			if len(keep) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(t, "[") && len(keep) == 0 {
			continue // install links ahead of the description
		}
		keep = append(keep, t)
		n += len(t)
		if n > 2000 {
			break
		}
	}
	return clip(strings.Join(keep, "\n\n"), 2500)
}

func bullets(text string) []string {
	var out []string
	var cur []string
	for _, line := range strings.Split(text, "\n") {
		if listItem.MatchString(line) {
			if len(cur) > 0 {
				out = append(out, strings.Join(cur, " "))
			}
			cur = []string{strings.TrimSpace(listItem.ReplaceAllString(line, ""))}
		} else if t := strings.TrimSpace(line); t != "" && cur != nil {
			cur = append(cur, t)
		}
	}
	if len(cur) > 0 {
		out = append(out, strings.Join(cur, " "))
	}
	return out
}

// clip shortens text to about n bytes at a word boundary.
func clip(text string, n int) string {
	if len(text) <= n {
		return text
	}
	cut := strings.LastIndex(text[:n], " ")
	if cut < n/2 {
		cut = n
	}
	for cut > 0 && !utf8Start(text[cut]) {
		cut--
	}
	return strings.TrimSpace(text[:cut]) + " …"
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
