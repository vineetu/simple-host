package brain

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixtureRepos makes two small git checkouts shaped like the real ones.
func fixtureRepos(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	host := filepath.Join(root, "host")
	ent := filepath.Join(root, "ent")
	parity := "# PARITY\n\n## Feature table\n\n| Area | Hosted | Enterprise | Status |\n|---|---|---|---|\n" +
		"| WordPress import from the owner's export (2026-10-14) | `create_import_link` and `get_import_report` | Not built | `gap → enterprise` |\n" +
		"| Site passcode | one passcode per site | same | `same` |\n" +
		"| Sign-in | email codes and Google | OIDC only | `different on purpose` (companies sign in their own way) |\n\n## Security fixes checked across\n\n| Date | Repo | Fix |\n|---|---|---|\n| 2026-10-01 | hosted | x |\n"
	write := func(dir, name, body string) {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(host, "INTENT.md", "# INTENT\n\n## What this is, and why it exists\n\nSimple Host hosts static sites.\n\n"+
		"## Who uses it, and in what situation\n\n- A person and their agent.\n\n## What success looks like\n\n- It ships first try.\n\n"+
		"## The hackathon product, and the page that sells it\n\nsimple-hack.app is a hosted hackathon platform.\n\n**2026-10-03 owner decision — withdraw unreviewed skills.** Reason here.\n\n"+
		"## Non-goals\n\n- Per-page locks.\n- Server code.\n\n## Decisions already made\n\n"+
		"- **2026-10-14. Visitors may post without signing in where the owner opts in, spam-checked by\n  Cloudflare Turnstile.** A comments preset. Reason: anonymous posting, never home-built.\n"+
		"- **2026-10-16. Simple Hack for companies is the Docker package, not Kubernetes.** Because one small server is enough.\n"+
		"- an undated item ends the one before\n")
	write(host, "FEATURES.md", "# FEATURES\n\n## 1. Sites and deploy\n\nDeploy a tarball.\n\n### WordPress import\n\nThe converter `internal/wpimport` turns a WordPress export into a site.\n")
	write(host, "PARITY.md", parity)
	write(host, "CHANGELOG.md", "# Changelog\n\n## 2026-10-16: Simple Hack for companies\n\n- Company sign-in.\n- A /companies page\n  with a film.\n\n## 2026-09-01: Old\n\n- Old thing.\n")
	write(host, "internal/handler/static/hack-llms.txt", "Simple Hack (https://simple-hack.app): hackathons for everyone.\n\nSimple Hack for companies: the self-hosted package.\n")
	write(host, "docs/platforms/README.md", "# Where it runs\n\nThe small box needs a disk.\n")
	write(host, ".env", "SECRET=do-not-read\n")
	write(host, "docs/platforms/leak.md", "# Leak\n\nkey shat_AAAAAAAAAAAAAAAAAAAAAAAAAAAA and sk-ant-0123456789abcdefghijk end\n")
	write(ent, "INTENT.md", "# INTENT\n\n## What this is, and why it exists\n\nEnterprise gives everyone a place.\n\n## Non-goals\n\n- Short names.\n\n## Decisions already made\n\n- **2026-10-15. Meta Muse on Enterprise.** Because companies use it.\n")
	write(ent, "FEATURES.md", "# FEATURES\n\n## 1. Identity: OIDC sign-in\n\nOIDC only.\n")
	write(ent, "PARITY.md", parity)
	write(ent, "CHANGELOG.md", "# Changelog\n\n## Unreleased\n\n- Coming.\n\n## v0.9.7 — 2026-10-15\n\n- Muse.\n")
	// A symlink to a file outside the allowlist must not be followed.
	if err := os.Symlink(filepath.Join(host, ".env"), filepath.Join(host, "docs/platforms/env.md")); err != nil {
		t.Fatal(err)
	}
	// Nor a symlinked directory that an allowlisted glob walks into.
	outside := filepath.Join(root, "outside")
	write(outside, "leaked.md", "# Leaked\n\nOUTSIDE-THE-REPO\n")
	if err := os.MkdirAll(filepath.Join(host, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(host, "docs/designs")); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{host, ent} {
		for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "x"}, {"tag", "v0.1.0"}} {
			cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v %s", args, err, out)
			}
		}
	}
	return host, ent
}

func buildFixture(t *testing.T) (*Store, string) {
	t.Helper()
	host, ent := fixtureRepos(t)
	b, err := Build(host, ent, time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "bundle.json")
	if err := WriteFile(b, out); err != nil {
		t.Fatal(err)
	}
	return Open(out), out
}

func TestBuildParsesSources(t *testing.T) {
	host, ent := fixtureRepos(t)
	b, err := Build(host, ent, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range b.Docs {
		if !Allowed(d.Path) {
			t.Errorf("bundle holds %s, outside the allowlist", d.Path)
		}
		if strings.Contains(d.Path, ".env") || strings.HasSuffix(d.Path, "env.md") {
			t.Errorf("bundle holds %s", d.Path)
		}
		for _, s := range d.Sections {
			if strings.Contains(s.Text, "OUTSIDE-THE-REPO") {
				t.Errorf("%s was read through a symlinked directory", d.Path)
			}
			if strings.Contains(s.Text, "shat_AAAA") || strings.Contains(s.Text, "sk-ant-") || strings.Contains(s.Text, "do-not-read") {
				t.Errorf("%s keeps a secret-shaped string: %q", d.Path, s.Text)
			}
		}
	}
	if len(b.Parity) != 3 || b.Parity[0].State != "gap_enterprise" || b.Parity[1].State != "same" || b.Parity[2].State != "different_on_purpose" {
		t.Fatalf("parity rows: %+v", b.Parity)
	}
	if b.Parity[0].Date != "2026-10-14" || !b.ParityCopiesMatch {
		t.Errorf("parity date %q, copies match %v", b.Parity[0].Date, b.ParityCopiesMatch)
	}
	titles := map[string]Decision{}
	for _, d := range b.Decisions {
		titles[d.Title] = d
	}
	turnstile, ok := titles["Visitors may post without signing in where the owner opts in, spam-checked by Cloudflare Turnstile"]
	if !ok || turnstile.Date != "2026-10-14" || !strings.Contains(turnstile.Text, "never home-built") {
		t.Fatalf("wrapped decision lead not parsed: %+v", b.Decisions)
	}
	if strings.Contains(turnstile.Text, "undated") {
		t.Error("an undated list item ran into the decision before it")
	}
	if d := titles["Simple Hack for companies is the Docker package, not Kubernetes"]; d.Product != "hack-companies" {
		t.Errorf("companies decision product %q", d.Product)
	}
	if d := titles["withdraw unreviewed skills"]; d.Date != "2026-10-03" {
		t.Errorf("bold paragraph decision: %+v", d)
	}
	if len(b.Decisions) != 4 || b.Decisions[0].Date != "2026-10-16" {
		t.Errorf("decisions %d, newest %s", len(b.Decisions), b.Decisions[0].Date)
	}
	if b.Changes[0].Date != "" || b.Changes[0].Repo != RepoEnterprise {
		t.Errorf("Unreleased should sort first: %+v", b.Changes[0])
	}
	if c := b.Changes[1]; c.Date != "2026-10-16" || len(c.Items) != 2 || c.Items[1] != "A /companies page with a film." {
		t.Errorf("change items: %+v", c)
	}
	if len(b.Products) != 6 || b.Products[0].What != "Simple Host hosts static sites." || len(b.Products[0].NonGoals) != 2 {
		t.Errorf("products: %+v", b.Products[0])
	}
}

func TestAllowedRefusesTraversal(t *testing.T) {
	for _, p := range []string{
		"simple-host/../etc/passwd", "simple-host/.env", "/etc/passwd", "simple-host/docs/designs/../../.env",
		"simple-host/", "other/INTENT.md", "simple-host/docs/designs/x.md/../../../INTENT.md", "simple-host\\INTENT.md",
		"simple-host-enterprise/docs/install.md", "simple-host/cmd/server/main.go",
		"chhota-console/CHANGELOG.md", "simple-host-travel/docs/migrate-chhota-break.md",
		"simple-host-travel/docs/verification-report.md", "simple-host-travel/docs/reviews/review.md",
	} {
		if Allowed(p) {
			t.Errorf("Allowed(%q) = true", p)
		}
	}
	for _, p := range []string{"chhota-console/README.md", "simple-host-travel/docs/install.md", "simple-host/INTENT.md", "simple-host/docs/designs/address-families.md", "simple-host-enterprise/PARITY.md", "simple-host/docs/history/simple-hack-release-v0.8.10.md"} {
		if !Allowed(p) {
			t.Errorf("Allowed(%q) = false", p)
		}
	}
}

func TestGetDocOnlyReadsTheBundle(t *testing.T) {
	s, _ := buildFixture(t)
	for _, p := range []string{"../../etc/passwd", "/etc/passwd", "simple-host/.env", "simple-host/docs/platforms/env.md", "simple-host/docs/designs/../../.env"} {
		if _, err := s.Call("get_doc", map[string]any{"path": p}); err == nil {
			t.Errorf("get_doc %q answered", p)
		}
	}
	res, err := s.Call("get_doc", map[string]any{"path": "simple-host/INTENT.md#decisions-already-made"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Structured["text"].(string), "Turnstile") || strings.Contains(res.Structured["text"].(string), "Per-page locks") {
		t.Errorf("section text: %s", res.Structured["text"])
	}
}

func TestQueries(t *testing.T) {
	s, _ := buildFixture(t)
	res, err := s.Call("feature_lookup", map[string]any{"query": "WordPress importer"})
	if err != nil {
		t.Fatal(err)
	}
	rows := res.Structured["parity"].([]any)
	if len(rows) == 0 || rows[0].(map[string]any)["state"] != "gap_enterprise" {
		t.Errorf("WordPress parity: %v", rows)
	}
	if f := res.Structured["features"].([]any); len(f) == 0 || f[0].(map[string]any)["name"] != "WordPress import" {
		t.Errorf("features: %v", f)
	}
	res, err = s.Call("decisions", map[string]any{"query": "Turnstile"})
	if err != nil || !strings.Contains(res.Text, "2026-10-14") {
		t.Errorf("decisions: %v %s", err, res.Text)
	}
	res, err = s.Call("decisions", map[string]any{"since": "2026-10-15"})
	if err != nil || res.Structured["total"] != 2 {
		t.Errorf("decisions since: %v %v", err, res.Structured["total"])
	}
	res, err = s.Call("recent_changes", map[string]any{"since": "2026-10-10", "product": "hack"})
	if err != nil || len(res.Structured["changes"].([]any)) != 1 {
		t.Errorf("recent changes: %v %v", err, res.Structured["changes"])
	}
	res, err = s.Call("product_overview", map[string]any{"product": "all"})
	if err != nil || len(res.Structured["products"].([]any)) != 6 {
		t.Errorf("overview: %v", err)
	}
	if res.Structured["freshness"].(map[string]any)["built_at"] != "2026-10-16T12:00:00Z" {
		t.Errorf("freshness: %v", res.Structured["freshness"])
	}
	for _, bad := range []map[string]any{{"since": "yesterday"}, {"limit": 0.5}, {"product": "nope"}} {
		if _, err := s.Call("decisions", bad); err == nil {
			t.Errorf("decisions %v accepted", bad)
		}
	}
}

func TestStoreSwapsBundleWithoutRestart(t *testing.T) {
	s, out := buildFixture(t)
	s.checkEvery = 0
	res, _ := s.Call("parity", map[string]any{})
	if len(res.Structured["rows"].([]any)) != 3 {
		t.Fatal("first bundle")
	}
	b, _ := load(out)
	b.Parity = b.Parity[:1]
	b.BuiltAt = b.BuiltAt.Add(time.Hour)
	if err := WriteFile(b, out); err != nil {
		t.Fatal(err)
	}
	// A different size is enough to notice even within one mtime tick.
	res, _ = s.Call("parity", map[string]any{})
	if n := len(res.Structured["rows"].([]any)); n != 1 {
		t.Errorf("after swap: %d rows", n)
	}
	// A broken bundle keeps the last good one serving.
	if err := os.WriteFile(out, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := s.Call("parity", map[string]any{})
	if err != nil || len(res.Structured["rows"].([]any)) != 1 {
		t.Errorf("broken bundle replaced the good one: %v", err)
	}
	// No temp files are left beside the bundle.
	entries, _ := os.ReadDir(filepath.Dir(out))
	if len(entries) != 1 {
		t.Errorf("left behind: %v", entries)
	}
}

func TestStoreNotReady(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "missing.json"))
	if _, err := s.Call("search", map[string]any{"query": "x"}); err != ErrNotReady {
		t.Errorf("err = %v", err)
	}
}

func TestBundleRefusesPathsOffTheAllowlist(t *testing.T) {
	_, out := buildFixture(t)
	b, _ := load(out)
	b.Docs = append(b.Docs, Doc{Path: "simple-host/.env", Repo: RepoHost})
	if err := WriteFile(b, out); err != nil {
		t.Fatal(err)
	}
	if _, err := load(out); err == nil {
		t.Error("a bundle naming a file off the allowlist loaded")
	}
}

// fixtureLocalSource archives master, then dirties the checkout to prove that
// Build reads only the extracted files and takes metadata from the named ref.
func fixtureLocalSource(t *testing.T, repo string, files map[string]string) LocalSource {
	t.Helper()
	dir, extracted := t.TempDir(), t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"symbolic-ref", "HEAD", "refs/heads/master"}, {"add", "-A"}, {"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "fixture"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	cmd := exec.Command("bash", "-o", "pipefail", "-c", `git -C "$1" archive master | tar -x -C "$2"`, "archive", dir, extracted)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("archive: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "INTENT.md"), []byte("uncommittedmarker"), 0o644); err != nil {
		t.Fatal(err)
	}
	return LocalSource{Repo: repo, RepoDir: dir, Branch: "master", FilesDir: extracted}
}

func TestBuildLocalSources(t *testing.T) {
	host, ent := fixtureRepos(t)
	console := fixtureLocalSource(t, RepoConsole, map[string]string{
		"INTENT.md":   "# INTENT\n\n- **2026-10-23. Consolechoice.** Simple Hack keyword must not change the product.\n",
		"FEATURES.md": "# FEATURES\n\n## Consoleuniquefeature\n\nConsoleuniquefeature is live.\n",
		"README.md":   "# Console\n",
		"PARITY.md":   "excluded",
	})
	travel := fixtureLocalSource(t, RepoTravel, map[string]string{
		"INTENT.md":                    "# INTENT\n\n- **2026-10-23. Travelchoice.** A self-hosted plan.\n",
		"REQUIREMENTS.md":              "# REQUIREMENTS\n\nTraveluniquerequirement is planned.\n",
		"CHANGELOG.md":                 "# Changelog\n\n## 2026-10-23: Travel plan\n\n- Self-hosted planning.\n",
		"docs/STATUS.md":               "# Status\n\nPlanning.\n",
		"docs/install.md":              "# Install\n\nPlanned install.\n",
		"docs/migrate-chhota-break.md": "excluded",
		"docs/verification.md":         "excluded",
		"docs/reviews/review.md":       "excluded",
	})
	b, err := Build(host, ent, time.Now(), console, travel)
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{RepoConsole: "Chhota Break console, live today", RepoTravel: "Simple Host for Travel, open-source plan, not live"}
	counts := map[string]int{}
	for _, d := range b.Docs {
		counts[d.Repo]++
		if !Allowed(d.Path) || d.Label != labels[d.Repo] {
			t.Errorf("doc: %+v", d)
		}
		if strings.Contains(docText(d), "uncommittedmarker") {
			t.Errorf("read working tree: %s", d.Path)
		}
	}
	if counts[RepoConsole] != 3 || counts[RepoTravel] != 5 {
		t.Fatalf("doc counts: %v", counts)
	}
	for _, s := range b.Sources {
		if s.Label != labels[s.Repo] || s.Commit == "" || s.CommitTime.IsZero() {
			t.Errorf("source: %+v", s)
		}
	}
	if len(b.Sources) != 4 || len(b.Products) != 6 {
		t.Fatalf("sources/products: %d/%d", len(b.Sources), len(b.Products))
	}
	out := filepath.Join(t.TempDir(), "bundle.json")
	if err := WriteFile(b, out); err != nil {
		t.Fatal(err)
	}
	store := Open(out)
	for repo, query := range map[string]string{RepoConsole: "Consoleuniquefeature", RepoTravel: "Traveluniquerequirement"} {
		res, err := store.Call("search", map[string]any{"query": query})
		if err != nil {
			t.Fatal(err)
		}
		hits := res.Structured["results"].([]any)
		if len(hits) != 1 || hits[0].(map[string]any)["label"] != labels[repo] || !strings.Contains(res.Text, "- ["+labels[repo]+"] ") {
			t.Errorf("search %s: %+v", repo, res)
		}
		res, err = store.Call("get_doc", map[string]any{"path": repo + "/INTENT.md"})
		if err != nil || res.Structured["label"] != labels[repo] || !strings.HasPrefix(res.Text, "["+labels[repo]+"] ") {
			t.Errorf("get_doc: %+v %v", res, err)
		}
	}
	res, err := store.Call("feature_lookup", map[string]any{"query": "Consoleuniquefeature"})
	if err != nil {
		t.Fatal(err)
	}
	features := res.Structured["features"].([]any)
	if len(features) != 1 || features[0].(map[string]any)["label"] != labels[RepoConsole] || features[0].(map[string]any)["edition"] != labels[RepoConsole] || !strings.Contains(res.Text, "- ["+labels[RepoConsole]+"] ") {
		t.Errorf("feature lookup: %+v", res)
	}
	for repo, product := range map[string]string{RepoConsole: "console", RepoTravel: "travel"} {
		res, err := store.Call("decisions", map[string]any{"product": product})
		if err != nil {
			t.Fatal(err)
		}
		items := res.Structured["decisions"].([]any)
		if len(items) != 1 {
			t.Fatalf("decisions %s: %+v", product, res)
		}
		m := items[0].(map[string]any)
		if m["repo"] != repo || m["product"] != product || m["label"] != labels[repo] || !strings.Contains(res.Text, "- ["+labels[repo]+"] ") {
			t.Errorf("decision: %+v", res)
		}
	}
	res, err = store.Call("recent_changes", map[string]any{"product": "travel", "since": "2026-10-01"})
	if err != nil {
		t.Fatal(err)
	}
	changes := res.Structured["changes"].([]any)
	if len(changes) != 1 {
		t.Fatalf("travel changes: %+v", res)
	}
	m := changes[0].(map[string]any)
	if m["product"] != "travel" || m["repo"] != RepoTravel || m["label"] != labels[RepoTravel] || !strings.Contains(res.Text, "- ["+labels[RepoTravel]+"] ") {
		t.Errorf("change: %+v", res)
	}
}
