package main

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/hackhttp"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/internal/webassets"
)

func TestHackGetStartedSkills(t *testing.T) {
	assets := handler.NewPages(edition.ForKind(edition.Hack), "")
	first, err := assets.BuildHackSkillsZip()
	if err != nil {
		t.Fatal(err)
	}
	second, err := assets.BuildHackSkillsZip()
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("hosted skill ZIP changed between reads")
	}
	zr, err := zip.NewReader(bytes.NewReader(first), int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	tops := map[string]bool{}
	for _, f := range zr.File {
		name, _, ok := strings.Cut(f.Name, "/")
		if !ok {
			t.Fatalf("not a skill folder: %s", f.Name)
		}
		tops[name] = true
		if strings.HasSuffix(f.Name, "/SKILL.md") {
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(r)
			r.Close()
			if err != nil {
				t.Fatal(err)
			}
			want, err := webassets.HackSkills.ReadFile("skills/" + f.Name)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(data, assets.SkillServedText(f.Name, want)) {
				t.Fatalf("%s differs from served canonical source", f.Name)
			}
		}
	}
	want := map[string]bool{}
	for _, name := range handler.HackSkillNames {
		want[name] = true
	}
	if !reflect.DeepEqual(tops, want) {
		t.Fatalf("ZIP skill folders %v, want %v", tops, want)
	}

	mux := http.NewServeMux()
	hackhttp.RegisterHackGetStarted(mux, assets)
	assets.RegisterSkillsHub(mux, "https://simple-hack.app")
	mux.HandleFunc("GET /skills.zip", func(w http.ResponseWriter, r *http.Request) { assets.ServeSkillsZip(w, r, true) })
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRecorder()
		mux.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		return r
	}
	if page := get("/get-started"); page.Code != 200 || !strings.Contains(page.Body.String(), "Run, join or judge a hackathon on the website or in your AI app.") {
		t.Fatalf("get-started: %d", page.Code)
	}
	if alias := get("/skills"); alias.Code != http.StatusMovedPermanently || alias.Header().Get("Location") != "/get-started" {
		t.Fatalf("skills alias: %d", alias.Code)
	}
	if download := get("/hack-skills.zip"); download.Code != 200 || !bytes.Equal(download.Body.Bytes(), first) {
		t.Fatalf("hosted ZIP: %d", download.Code)
	}
	if download := get("/skills.zip"); download.Code != 200 || !bytes.Equal(download.Body.Bytes(), first) {
		t.Fatalf("standard ZIP: %d", download.Code)
	}
	for _, version := range []string{"0.2.0", "0.2.1", "0.2.2", "0.2.3", "0.2.4", "0.2.5"} {
		name := "simple-hack-skills-only-" + version + ".zip"
		if download := get("/" + name); download.Code != http.StatusGone {
			t.Fatalf("withdrawn ChatGPT ZIP %s: %d", version, download.Code)
		}
	}
	name := "simple-hack-skills-only-0.2.6.zip"
	primary, err := webassets.HackFiles.ReadFile("site/downloads/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if download := get("/" + name); download.Code != 200 || !bytes.Equal(download.Body.Bytes(), primary) {
		t.Fatalf("current ChatGPT ZIP: %d", download.Code)
	}
	if catalog := get("/v1/skills"); catalog.Code != 200 || !strings.Contains(catalog.Body.String(), `"count":5`) || !strings.Contains(catalog.Body.String(), `"plugin":"simple-hack"`) {
		t.Fatalf("hosted skill catalog: %d %s", catalog.Code, catalog.Body.String())
	}
	if other := get("/v1/skills/connect-domain/SKILL.md"); other.Code != http.StatusNotFound {
		t.Fatalf("foreign skill exposed on Simple Hack: %d", other.Code)
	}
	for _, name := range handler.HackSkillNames {
		if raw := get("/v1/skills/" + name + "/SKILL.md"); raw.Code != 200 || !strings.Contains(raw.Body.String(), "name: "+name) {
			t.Fatalf("raw %s: %d", name, raw.Code)
		}
	}
}

func TestHackGetStartedOnboarding(t *testing.T) {
	assets := handler.NewPages(edition.ForKind(edition.Hack), "")
	mux := http.NewServeMux()
	hackhttp.RegisterHackGetStarted(mux, assets)
	r := httptest.NewRecorder()
	mux.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/get-started", nil))
	page := r.Body.String()
	last := -1
	for _, heading := range []string{"Run, join or judge a hackathon on the website or in your AI app.", ">Connect your AI app</h2>", ">Organiser</h2>", ">Participant</h2>", ">Judge</h2>", ">Pick your AI</h2>", ">What happens next</h2>", "Other ways to install"} {
		at := strings.Index(page, heading)
		if at <= last {
			t.Fatalf("missing or out-of-order heading %q", heading)
		}
		last = at
	}
	// One way in that always works: the MCP address, once, prominently, near
	// the top.
	if !strings.Contains(page, `id="mcp-url" data-mcp-address>https://simple-hack.app/mcp</code>`) {
		t.Fatal("top of page must show the MCP address in one copy box")
	}
	// Hosted keeps every existing plugin/directory option (SIM connect-mcp
	// rule 1: nothing is removed, the URL option is only ADDED alongside it).
	for _, want := range []string{"Browse connectors", "chatgpt.com/plugins", "is in review", "Upload plugin archive", "Claude setup guide"} {
		if !strings.Contains(page, want) {
			t.Errorf("hosted get-started must still offer %q (plugin options stay on hosted)", want)
		}
	}
	// Hosted restores the baseline row set exactly (SIM connect-mcp review
	// finding A): Claude and ChatGPT are their own rows, and Claude Code has
	// no row of its own here (unlike install.html) — it shares the combined
	// "Claude Code / Codex / other coding agents" row baseline always had,
	// checked separately below.
	for _, id := range []string{"claude", "chatgpt"} {
		_, rest, found := strings.Cut(page, `<details class="accordion" id="`+id+`" name="pick-ai">`)
		card, _, _ := strings.Cut(rest, "</details>")
		if !found {
			t.Fatalf("%s: missing from Pick your AI", id)
		}
		if !strings.Contains(card, "https://simple-hack.app/mcp") || !strings.Contains(card, "<b>Allow</b>") {
			t.Errorf("%s must have the connector address and consent instructions", id)
		}
		if id != "claude" && !strings.Contains(card, "Google or an email code") {
			t.Errorf("%s must describe sign-in", id)
		}
		switch id {
		case "chatgpt":
			if !strings.Contains(card, "Developer mode") || !strings.Contains(card, "MCP server URL") || !strings.Contains(card, "OAuth") {
				t.Error("ChatGPT must describe the developer-mode custom connector steps")
			}
			if !strings.Contains(card, "Upload plugin archive") {
				t.Error("ChatGPT must still offer the plugin archive upload, alongside the URL option")
			}
		case "claude":
			if !strings.Contains(card, "Browse connectors") || !strings.Contains(card, "Claude setup guide") {
				t.Error("Claude must still offer the hosted connector directory, alongside the URL option")
			}
		}
	}
	_, codingRest, foundCoding := strings.Cut(page, `<details class="accordion" id="coding-agents" name="pick-ai">`)
	codingCard, _, _ := strings.Cut(codingRest, "</details>")
	if !foundCoding {
		t.Fatal("coding-agents: missing from Pick your AI (Claude Code's baseline row)")
	}
	if !strings.Contains(codingCard, "Claude Code") || !strings.Contains(codingCard, "https://simple-hack.app/mcp") || !strings.Contains(codingCard, "<b>Allow</b>") {
		t.Error("coding-agents must name Claude Code and give the connector address and consent instructions")
	}
	// Hosted keeps baseline's flat row list (SIM connect-mcp review finding
	// A): grok/copilot/muse/gemini/mistral are their own top-level rows,
	// never collapsed into the self-hosted-only "Other AI apps" group.
	for _, id := range []string{"grok", "copilot", "muse", "gemini", "mistral"} {
		if !strings.Contains(page, `<details class="accordion" id="`+id+`" name="pick-ai">`) {
			t.Errorf("%s missing from Pick your AI", id)
		}
	}
	if !strings.Contains(page, "meta-muse") {
		t.Error("Meta Muse must give its Client ID")
	}
	if strings.Contains(page, `id="other-ai-apps"`) {
		t.Error("hosted must not collapse rows into the self-hosted-only Other AI apps group")
	}
	_, other, _ := strings.Cut(page, `<details class="other" id="other-installs">`)
	other, _, _ = strings.Cut(other, "</details>")
	if other == "" || strings.Contains(other, "<summary open") || strings.Contains(page, `id="other-installs" open`) {
		t.Fatal("other installs must be collapsed")
	}
	if !strings.Contains(other, "Optional") {
		t.Error("skill install must be labeled optional, not the way to connect")
	}
	for _, url := range []string{"/skills.zip", "/hack-skills.zip", "/v1/skills/run-hackathon/references/organiser-api.md", "/v1/skills/website-deploy/references/storage.md"} {
		if !strings.Contains(other, `href="`+url+`"`) {
			t.Errorf("download/reference %s missing from other installs", url)
		}
	}
	for _, name := range handler.HackSkillNames {
		if !strings.Contains(other, `href="/v1/skills/`+name+`/SKILL.md"`) {
			t.Errorf("skill %s missing from other installs", name)
		}
	}
	for _, stale := range []string{"Choose a skill", "Read run-hackathon", "version 0.27", "prepared this package"} {
		if strings.Contains(page, stale) {
			t.Errorf("stale onboarding copy %q", stale)
		}
	}
	for _, want := range []string{"team.event.simple-hack.app", "https://simple-host.app/hackathons", "ranks and overall scores are public by default", "After results are published", `id="prompt-organiser"`, `id="prompt-participant"`, `id="prompt-judge"`, `class="sh-header sh-hack"`, `class="sh-footer"`, "/hack-ink.css?v="} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
	if !strings.Contains(page, `nonce="`) || strings.Contains(page, "<!--sh:") {
		t.Fatal("page must use the shared chrome and CSP nonce")
	}
}

// TestHackGetStartedOnboardingSelfHosted is TestHackGetStartedOnboarding's
// mirror image for rule 2 of the connect-MCP work: a self-hosted instance
// shows only its own https://<host>/mcp, never a plugin, ZIP, directory or
// app-review mention, and never the hosted service's own domain.
func TestHackGetStartedOnboardingSelfHosted(t *testing.T) {
	assets := handler.NewPages(edition.ForKind(edition.Hack), "http://hack.example.test:18201")
	mux := http.NewServeMux()
	hackhttp.RegisterHackGetStarted(mux, assets)
	r := httptest.NewRecorder()
	mux.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/get-started", nil))
	page := r.Body.String()

	if !strings.Contains(page, `id="mcp-url" data-mcp-address>http://hack.example.test:18201/mcp</code>`) {
		t.Fatal("self-hosted top of page must show this install's own MCP address")
	}
	for _, stale := range []string{"plugin", "Plugin", "Browse connectors", "chatgpt.com/plugins", "is in review", "Upload plugin archive", "Claude setup guide", "simple-hack.app", "simple-host.app"} {
		if strings.Contains(page, stale) {
			t.Errorf("self-hosted get-started must not mention %q: no plugin mentions, no hosted domain", stale)
		}
	}
	for _, id := range []string{"claude", "chatgpt", "claude-code"} {
		_, rest, found := strings.Cut(page, `<details class="accordion" id="`+id+`" name="pick-ai">`)
		card, _, _ := strings.Cut(rest, "</details>")
		if !found {
			t.Fatalf("%s: missing from Pick your AI", id)
		}
		if !strings.Contains(card, "http://hack.example.test:18201/mcp") {
			t.Errorf("%s must point at this install's own address", id)
		}
	}
}
