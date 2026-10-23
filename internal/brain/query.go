package brain

import (
	"fmt"
	"path"
	"strings"
	"time"
)

// Products lists the product ids accepted by query filters.
var Products = []string{"host", "hack", "enterprise", "selfhost-host", "selfhost-hack", "hack-companies", "console", "travel", "all"}

// ArgError is a bad argument: the caller's to correct.
type ArgError struct{ msg string }

func (e ArgError) Error() string { return e.msg }

func argErr(format string, a ...any) error { return ArgError{fmt.Sprintf(format, a...)} }

// Result is one query's answer: a short text for the model and the
// structured value (which matches the tool's output schema).
type Result struct {
	Text       string
	Structured map[string]any
}

// Call runs one named query with JSON-shaped arguments. The names are the MCP
// tool names; the REST mirror uses the same ones.
func (s *Store) Call(name string, args map[string]any) (Result, error) {
	b, ix, err := s.current()
	if err != nil {
		return Result{}, err
	}
	q := querier{b: b, ix: ix, now: time.Now().UTC()}
	switch name {
	case "product_overview":
		return q.overview(args)
	case "feature_lookup":
		return q.featureLookup(args)
	case "parity":
		return q.parity(args)
	case "decisions":
		return q.decisions(args)
	case "recent_changes":
		return q.recentChanges(args)
	case "search":
		return q.search(args)
	case "get_doc":
		return q.getDoc(args)
	}
	return Result{}, argErr("unknown tool %q", name)
}

// Freshness says when the loaded bundle was built and from which commits.
func (s *Store) Freshness() (map[string]any, error) {
	b, _, err := s.current()
	if err != nil {
		return nil, err
	}
	return freshness(b), nil
}

type querier struct {
	b   *Bundle
	ix  *index
	now time.Time
}

func freshness(b *Bundle) map[string]any {
	sources := make([]any, 0, len(b.Sources))
	for _, s := range b.Sources {
		sources = append(sources, map[string]any{"repo": s.Repo, "label": s.Label, "commit": s.Commit, "commit_time": s.CommitTime.Format(time.RFC3339)})
	}
	return map[string]any{"built_at": b.BuiltAt.Format(time.RFC3339), "sources": sources, "parity_copies_match": b.ParityCopiesMatch}
}

// ---- argument helpers ----------------------------------------------------------

func optString(args map[string]any, key string, max int) (string, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return "", nil
	}
	v, ok := raw.(string)
	if !ok {
		return "", argErr("%s must be a string", key)
	}
	v = strings.TrimSpace(v)
	if len(v) > max {
		return "", argErr("%s is too long (at most %d characters)", key, max)
	}
	return v, nil
}

func needString(args map[string]any, key string, max int) (string, error) {
	v, err := optString(args, key, max)
	if err == nil && v == "" {
		err = argErr("%s is required", key)
	}
	return v, err
}

func optLimit(args map[string]any, def, max int) (int, error) {
	raw, ok := args["limit"]
	if !ok || raw == nil {
		return def, nil
	}
	f, ok := raw.(float64)
	if !ok {
		if i, isInt := raw.(int); isInt {
			f = float64(i)
		} else {
			return 0, argErr("limit must be a number")
		}
	}
	n := int(f)
	if float64(n) != f || n < 1 || n > max {
		return 0, argErr("limit must be a whole number from 1 to %d", max)
	}
	return n, nil
}

// optSince reads a date (2026-10-13) or a relative span (3d, 2w).
func (q querier) optSince(args map[string]any) (string, error) {
	v, err := optString(args, "since", 20)
	if err != nil || v == "" {
		return "", err
	}
	if _, err := time.Parse("2006-01-02", v); err == nil {
		return v, nil
	}
	var n int
	var unit string
	if _, err := fmt.Sscanf(v, "%d%s", &n, &unit); err == nil && n >= 0 && n <= 3650 {
		switch unit {
		case "d", "day", "days":
			return q.now.AddDate(0, 0, -n).Format("2006-01-02"), nil
		case "w", "week", "weeks":
			return q.now.AddDate(0, 0, -7*n).Format("2006-01-02"), nil
		}
	}
	return "", argErr("since must be a date like 2026-10-13 or a span like 3d or 2w")
}

func (q querier) optProduct(args map[string]any, allowAll bool) (string, error) {
	v, err := optString(args, "product", 40)
	if err != nil || v == "" {
		return v, err
	}
	for _, p := range Products {
		if v == p && (allowAll || p != "all") {
			return v, nil
		}
	}
	return "", argErr("product must be one of %s", strings.Join(Products, ", "))
}

// productMatch: entries tagged with a self-hosted or companies product also
// belong to their parent product when filtering.
func productMatch(want, have string) bool {
	if want == "" || want == "all" || want == have {
		return true
	}
	switch want {
	case "hack":
		return have == "selfhost-hack" || have == "hack-companies"
	case "selfhost-hack":
		return have == "hack-companies"
	}
	return false
}

// ---- tools -----------------------------------------------------------------------

func (q querier) overview(args map[string]any) (Result, error) {
	product, err := q.optProduct(args, true)
	if err != nil {
		return Result{}, err
	}
	if product == "" {
		return Result{}, argErr("product is required: one of %s", strings.Join(Products, ", "))
	}
	var products []any
	var lines []string
	for _, p := range q.b.Products {
		if product != "all" && p.ID != product {
			continue
		}
		recent := []any{}
		for _, d := range q.b.Decisions {
			if len(recent) == 5 {
				break
			}
			if productMatch(p.ID, d.Product) {
				recent = append(recent, map[string]any{"date": d.Date, "title": d.Title, "source": d.Source})
			}
		}
		entry := map[string]any{
			"id": p.ID, "name": p.Name, "what": p.What, "who_for": p.WhoFor, "success": p.Success,
			"non_goals": p.NonGoals, "version": p.Version, "sources": p.Sources, "recent_decisions": recent,
		}
		if product == "all" {
			// One line each keeps the all-products answer short.
			entry["what"] = clip(p.What, 600)
			entry["who_for"] = clip(p.WhoFor, 400)
			entry["success"] = clip(p.Success, 400)
			if len(p.NonGoals) > 4 {
				entry["non_goals"] = p.NonGoals[:4]
			}
		}
		products = append(products, entry)
		if product == "all" {
			lines = append(lines, p.Name+": "+clip(collapse(p.What), 300)+" Version: "+p.Version+".")
			continue
		}
		lines = append(lines, p.Name, "What it is: "+clip(collapse(p.What), 1500))
		if p.WhoFor != "" {
			lines = append(lines, "Who it is for: "+clip(collapse(p.WhoFor), 800))
		}
		if p.Success != "" {
			lines = append(lines, "Success: "+clip(collapse(p.Success), 600))
		}
		for i, ng := range p.NonGoals {
			if i == 6 {
				break
			}
			lines = append(lines, "Non-goal: "+clip(ng, 220))
		}
		lines = append(lines, "Version: "+p.Version+".")
		for _, d := range recent {
			m := d.(map[string]any)
			lines = append(lines, "Decision "+m["date"].(string)+": "+m["title"].(string))
		}
	}
	fresh := freshness(q.b)
	text := strings.Join(lines, "\n") + "\nBundle built " + fresh["built_at"].(string) + " from " + q.commits() + "."
	return Result{Text: text, Structured: map[string]any{"products": products, "freshness": fresh}}, nil
}

func (q querier) commits() string {
	var parts []string
	for _, s := range q.b.Sources {
		parts = append(parts, labelPrefix(s.Label)+s.Repo+"@"+s.Commit)
	}
	return strings.Join(parts, " and ")
}

func (q querier) featureLookup(args map[string]any) (Result, error) {
	query, err := needString(args, "query", 200)
	if err != nil {
		return Result{}, err
	}
	limit, err := optLimit(args, 5, 15)
	if err != nil {
		return Result{}, err
	}
	hits := q.ix.search(query, func(r ref) bool {
		return path.Base(q.b.Docs[r.doc].Path) == "FEATURES.md" && q.b.Docs[r.doc].Sections[r.sec].Heading != ""
	})
	features := []any{}
	var lines []string
	for _, h := range hits {
		if len(features) == limit {
			break
		}
		d := q.b.Docs[h.ref.doc]
		s := d.Sections[h.ref.sec]
		edition := "Simple Host (hosted, small box and Simple Hack share this binary)"
		if d.Repo == RepoEnterprise {
			edition = "Simple Host Enterprise"
		}
		if d.Label != "" {
			edition = d.Label
		}
		features = append(features, map[string]any{
			"label": d.Label, "name": s.Heading, "edition": edition, "how_it_works": snippet(s.Text, query, 1500),
			"source": d.Path + "#" + s.ID,
		})
		lines = append(lines, "- "+labelPrefix(d.Label)+s.Heading+" ("+edition+", "+d.Path+"#"+s.ID+"): "+snippet(s.Text, query, 400))
	}
	rows := q.parityMatches(query, 5, 700)
	for _, r := range rows {
		m := r.(map[string]any)
		lines = append(lines, "- parity: "+m["area"].(string)+": "+m["status"].(string))
	}
	text := "No feature matched; try search with other words."
	if len(lines) > 0 {
		text = "Features and parity rows for \"" + query + "\":\n" + strings.Join(lines, "\n") + "\nRead a full section with get_doc."
	}
	return Result{Text: text, Structured: map[string]any{"query": query, "features": features, "parity": rows}}, nil
}

func parityEntry(r ParityRow, cell int) map[string]any {
	return map[string]any{
		"area": r.Area, "hosted": clip(r.Hosted, cell), "enterprise": clip(r.Enterprise, cell),
		"status": r.Status, "state": r.State, "date": r.Date,
	}
}

func (q querier) parityMatches(query string, limit, cell int) []any {
	type scored struct {
		row   ParityRow
		score float64
	}
	var all []scored
	for _, r := range q.b.Parity {
		// The area names the feature; it weighs three times the cells.
		sc := 3*textScore(query, r.Area) + textScore(query, r.Hosted+" "+r.Enterprise)
		if sc > 0 {
			all = append(all, scored{r, sc})
		}
	}
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].score > all[j-1].score; j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	out := []any{}
	for _, s := range all {
		if len(out) == limit {
			break
		}
		out = append(out, parityEntry(s.row, cell))
	}
	return out
}

func (q querier) parity(args map[string]any) (Result, error) {
	feature, err := optString(args, "feature", 200)
	if err != nil {
		return Result{}, err
	}
	counts := map[string]any{"same": 0, "different_on_purpose": 0, "gap_hosted": 0, "gap_enterprise": 0, "not_applicable": 0, "other": 0}
	for _, r := range q.b.Parity {
		n, _ := counts[r.State].(int)
		counts[r.State] = n + 1
	}
	meta := map[string]any{
		"editions": "Hosted means simple-host.app and the small box that anyone runs (the same binary; Simple Hack is the same binary too). Enterprise is the Kubernetes edition behind a company's own OIDC.",
		"source":   RepoHost + "/PARITY.md#feature-table",
	}
	if feature == "" {
		rows := []any{}
		for _, r := range q.b.Parity {
			rows = append(rows, parityEntry(r, 220))
		}
		text := fmt.Sprintf("%d parity rows: %v. Cells are shortened; call parity with a feature for the full row.", len(rows), counts)
		return Result{Text: text, Structured: map[string]any{"feature": "", "rows": rows, "counts": counts, "about": meta}}, nil
	}
	rows := q.parityMatches(feature, 8, 4000)
	var lines []string
	for _, r := range rows {
		m := r.(map[string]any)
		lines = append(lines, "- "+m["area"].(string)+": "+m["status"].(string)+"\n  Hosted: "+clip(m["hosted"].(string), 400)+"\n  Enterprise: "+clip(m["enterprise"].(string), 400))
	}
	text := "No parity row matched \"" + feature + "\"; call parity with no feature for the whole table."
	if len(lines) > 0 {
		text = "Parity rows for \"" + feature + "\", best match first:\n" + strings.Join(lines, "\n")
	}
	return Result{Text: text, Structured: map[string]any{"feature": feature, "rows": rows, "counts": counts, "about": meta}}, nil
}

func (q querier) decisions(args map[string]any) (Result, error) {
	since, err := q.optSince(args)
	if err != nil {
		return Result{}, err
	}
	query, err := optString(args, "query", 200)
	if err != nil {
		return Result{}, err
	}
	product, err := q.optProduct(args, true)
	if err != nil {
		return Result{}, err
	}
	limit, err := optLimit(args, 15, 50)
	if err != nil {
		return Result{}, err
	}
	type scored struct {
		d     Decision
		score float64
	}
	var picked []scored
	for _, d := range q.b.Decisions {
		if since != "" && d.Date < since {
			continue
		}
		if !productMatch(product, d.Product) {
			continue
		}
		sc := 1.0
		if query != "" {
			sc = 3*textScore(query, d.Title) + textScore(query, d.Text)
			if sc == 0 {
				continue
			}
		}
		picked = append(picked, scored{d, sc})
	}
	if query != "" {
		for i := 1; i < len(picked); i++ {
			for j := i; j > 0 && picked[j].score > picked[j-1].score; j-- {
				picked[j], picked[j-1] = picked[j-1], picked[j]
			}
		}
	}
	total := len(picked)
	out := []any{}
	var lines []string
	for _, p := range picked {
		if len(out) == limit {
			break
		}
		out = append(out, map[string]any{"label": sourceLabel[p.d.Repo], "repo": p.d.Repo, "date": p.d.Date, "title": p.d.Title, "text": clip(p.d.Text, 1800), "product": p.d.Product, "source": p.d.Source})
		lines = append(lines, "- "+labelPrefix(sourceLabel[p.d.Repo])+p.d.Date+" "+p.d.Title+" ("+p.d.Source+"): "+clip(collapse(p.d.Text), 500))
	}
	text := "No decision matched."
	if len(lines) > 0 {
		text = fmt.Sprintf("%d of %d decisions:\n%s", len(out), total, strings.Join(lines, "\n"))
	}
	return Result{Text: text, Structured: map[string]any{"since": since, "query": query, "total": total, "decisions": out}}, nil
}

func (q querier) recentChanges(args map[string]any) (Result, error) {
	since, err := q.optSince(args)
	if err != nil {
		return Result{}, err
	}
	if since == "" {
		since = q.now.AddDate(0, 0, -7).Format("2006-01-02")
	}
	product, err := q.optProduct(args, true)
	if err != nil {
		return Result{}, err
	}
	limit, err := optLimit(args, 30, 100)
	if err != nil {
		return Result{}, err
	}
	out := []any{}
	var lines []string
	for _, c := range q.b.Changes {
		if len(out) == limit {
			break
		}
		// Unreleased entries are always recent.
		if c.Date != "" && c.Date < since {
			continue
		}
		if !productMatch(product, c.Product) {
			continue
		}
		items := make([]string, 0, len(c.Items))
		for _, it := range c.Items {
			items = append(items, clip(it, 900))
		}
		date := c.Date
		if date == "" {
			date = "unreleased"
		}
		out = append(out, map[string]any{"label": sourceLabel[c.Repo], "date": date, "title": c.Title, "items": items, "repo": c.Repo, "product": c.Product, "source": c.Source})
		lines = append(lines, "- "+labelPrefix(sourceLabel[c.Repo])+date+" "+c.Title+" ("+c.Repo+"):")
		for i, it := range items {
			if i == 6 {
				lines = append(lines, fmt.Sprintf("  (%d more in the structured result)", len(items)-6))
				break
			}
			lines = append(lines, "  - "+clip(collapse(it), 220))
		}
	}
	text := "No changes since " + since + "."
	if len(lines) > 0 {
		text = "Changes since " + since + ", newest first:\n" + strings.Join(lines, "\n")
	}
	return Result{Text: text, Structured: map[string]any{"since": since, "product": product, "changes": out}}, nil
}

func (q querier) search(args map[string]any) (Result, error) {
	query, err := needString(args, "query", 200)
	if err != nil {
		return Result{}, err
	}
	limit, err := optLimit(args, 8, 25)
	if err != nil {
		return Result{}, err
	}
	results := []any{}
	var lines []string
	for _, h := range q.ix.search(query, nil) {
		if len(results) == limit {
			break
		}
		d := q.b.Docs[h.ref.doc]
		s := d.Sections[h.ref.sec]
		heading := s.Heading
		if heading == "" {
			heading = d.Title
		}
		results = append(results, map[string]any{
			"label": d.Label, "path": d.Path, "section": s.ID, "heading": heading,
			"snippet": snippet(s.Text, query, 500), "score": float64(int(h.score*100)) / 100,
		})
		lines = append(lines, "- "+labelPrefix(d.Label)+d.Path+"#"+s.ID+" ("+heading+"): "+snippet(s.Text, query, 300))
	}
	text := "Nothing matched; try fewer or other words."
	if len(lines) > 0 {
		text = "Best matches for \"" + query + "\" (read one with get_doc):\n" + strings.Join(lines, "\n")
	}
	return Result{Text: text, Structured: map[string]any{"query": query, "results": results}}, nil
}

// maxDocAnswer bounds one get_doc answer; a longer document answers its
// outline and the first part, and the caller asks for a section.
const maxDocAnswer = 60000

func (q querier) getDoc(args map[string]any) (Result, error) {
	p, err := needString(args, "path", 300)
	if err != nil {
		return Result{}, err
	}
	section, err := optString(args, "section", 300)
	if err != nil {
		return Result{}, err
	}
	p = strings.TrimPrefix(strings.SplitN(p, "#", 2)[0], "/")
	if i := strings.Index(args["path"].(string), "#"); i >= 0 && section == "" {
		section = strings.TrimSpace(args["path"].(string)[i+1:])
	}
	// Only a document the builder put in the bundle can be read; there is no
	// filesystem access here, so no path can reach anything else.
	var doc *Doc
	for i := range q.b.Docs {
		if q.b.Docs[i].Path == p {
			doc = &q.b.Docs[i]
		}
	}
	if doc == nil || !Allowed(p) {
		var paths []string
		for _, d := range q.b.Docs {
			if !strings.Contains(d.Path, "/docs/") {
				paths = append(paths, d.Path)
			}
		}
		return Result{}, argErr("%q is not one of the documents this server reads. Main ones: %s; search returns the others", p, strings.Join(paths, ", "))
	}
	outline := make([]any, 0, len(doc.Sections))
	for _, s := range doc.Sections {
		if s.Heading != "" {
			outline = append(outline, map[string]any{"id": s.ID, "heading": s.Heading, "level": s.Level})
		}
	}
	var sb strings.Builder
	found := section == ""
	if section != "" {
		want := anchor(strings.TrimPrefix(section, "#"))
		for i, s := range doc.Sections {
			if s.ID != strings.TrimPrefix(section, "#") && s.ID != want && !strings.EqualFold(s.Heading, section) {
				continue
			}
			found = true
			// The section and its subsections.
			for _, sub := range doc.Sections[i:] {
				if sub.ID != s.ID && sub.Level <= s.Level {
					break
				}
				writeSection(&sb, sub)
			}
			break
		}
		if !found {
			return Result{}, argErr("%s has no section %q; its sections are in the outline from get_doc without a section", p, section)
		}
	} else {
		for _, s := range doc.Sections {
			writeSection(&sb, s)
		}
	}
	text := sb.String()
	truncated := false
	if len(text) > maxDocAnswer {
		text, truncated = clip(text, maxDocAnswer), true
	}
	answer := labelPrefix(doc.Label) + text
	if truncated {
		answer += "\n\n[Shortened. Ask for one section by its id from the outline.]"
	}
	return Result{Text: answer, Structured: map[string]any{
		"label": doc.Label, "path": p, "section": section, "title": doc.Title, "text": text, "truncated": truncated, "outline": outline,
	}}, nil
}

func writeSection(sb *strings.Builder, s Section) {
	if s.Heading != "" {
		sb.WriteString(strings.Repeat("#", s.Level) + " " + s.Heading + "\n\n")
	}
	if s.Text != "" {
		sb.WriteString(s.Text + "\n\n")
	}
}

func labelPrefix(label string) string {
	if label == "" {
		return ""
	}
	return "[" + label + "] "
}
