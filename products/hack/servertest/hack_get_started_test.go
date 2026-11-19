package servertest

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	productedition "github.com/vsriram/simple-host/products/hack/edition"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/handler"
	"github.com/vsriram/simple-host/products/hack/hackhttp"
	hackassets "github.com/vsriram/simple-host/products/hack/webassets"
)

func TestHackGetStartedSkills(t *testing.T) {
	sh := chromeTestHandler(productedition.Hack)
	assets := hackhttp.NewAssets(sh.AssetServices(), "")
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
			want, err := hackassets.HackSkills.ReadFile("skills/" + f.Name)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(data, sh.Pages().SkillServedText(f.Name, want)) {
				t.Fatalf("%s differs from served canonical source", f.Name)
			}
		}
	}
	want := map[string]bool{}
	for _, name := range hackhttp.HackSkillNames {
		want[name] = true
	}
	if !reflect.DeepEqual(tops, want) {
		t.Fatalf("ZIP skill folders %v, want %v", tops, want)
	}

	mux := http.NewServeMux()
	hackhttp.RegisterHackGetStarted(mux, sh.Pages(), hackAssetsFor(sh))
	sh.Pages().RegisterSkillsHub(mux, "https://simple-hack.app")
	mux.HandleFunc("GET /skills.zip", assets.ServeHackSkillsZip())
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
	primary, err := hackassets.HackFiles.ReadFile("site/downloads/" + name)
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
	for _, name := range hackhttp.HackSkillNames {
		if raw := get("/v1/skills/" + name + "/SKILL.md"); raw.Code != 200 || !strings.Contains(raw.Body.String(), "name: "+name) {
			t.Fatalf("raw %s: %d", name, raw.Code)
		}
	}
}

func TestHackGetStartedOnboarding(t *testing.T) {
	sh := chromeTestHandler(productedition.Hack)
	mux := http.NewServeMux()
	hackhttp.RegisterHackGetStarted(mux, sh.Pages(), hackAssetsFor(sh))
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
	// Claude, ChatGPT and Claude Code stay the three primary apps.
	for _, id := range []string{"claude", "chatgpt", "claude-code"} {
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
	checkOtherAIApps(t, page, "https://simple-hack.app/mcp")
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
	for _, name := range hackhttp.HackSkillNames {
		if !strings.Contains(other, `href="/v1/skills/`+name+`/SKILL.md"`) {
			t.Errorf("skill %s missing from other installs", name)
		}
	}
	for _, stale := range []string{"Choose a skill", "Read run-hackathon", "version 0.27", "prepared this package"} {
		if strings.Contains(page, stale) {
			t.Errorf("stale onboarding copy %q", stale)
		}
	}
	for _, want := range []string{"team.event.simple-hack.app", "https://simple-hack.app/hackathons", "ranks and overall scores are public by default", "After results are published", `id="prompt-organiser"`, `id="prompt-participant"`, `id="prompt-judge"`, `class="sh-header sh-hack"`, `class="sh-footer"`, "/hack-ink.css?v="} {
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
	sh := chromeTestHandlerWithBase(productedition.Hack, "http://hack.example.test:18201")
	mux := http.NewServeMux()
	hackhttp.RegisterHackGetStarted(mux, sh.Pages(), hackAssetsFor(sh))
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
	checkOtherAIApps(t, page, "http://hack.example.test:18201/mcp")
}

func checkOtherAIApps(t *testing.T, page, address string) {
	t.Helper()
	_, group, found := strings.Cut(page, `<details class="other" id="other-ai-apps">`)
	if !found {
		t.Fatal("Other AI apps group missing")
	}
	last := -1
	for _, id := range []string{"grok", "copilot", "muse", "gemini", "mistral", "lovable", "bolt", "cursor", "other"} {
		marker := `<details class="accordion" id="` + id + `" name="other-ai">`
		at := strings.Index(group, marker)
		if at <= last {
			t.Fatalf("%s missing or out of order in Other AI apps", id)
		}
		last = at
		_, rest, _ := strings.Cut(group, marker)
		card, _, _ := strings.Cut(rest, "</details>")
		if !strings.Contains(card, address) || !strings.Contains(card, "<b>Allow</b>") {
			t.Errorf("%s must show this instance's address and consent instructions", id)
		}
	}
	if !strings.Contains(group, "meta-muse") || !strings.Contains(group, "Any other AI app") {
		t.Error("Other AI apps must keep Muse's Client ID and the generic option")
	}
	if strings.Contains(page, `id="other-coding"`) || strings.Contains(page, `id="coding-agents"`) {
		t.Error("old coding-agent rows must be replaced by the generic option")
	}
}

// A self-hosted Simple Hack names its own connector for Claude, never the
// hosted listing.
func TestSelfHostedHackGetStartedClaude(t *testing.T) {
	sh := chromeTestHandlerWithBase(productedition.Hack, "https://hack.example")
	mux := http.NewServeMux()
	hackhttp.RegisterHackGetStarted(mux, sh.Pages(), hackAssetsFor(sh))
	r := httptest.NewRecorder()
	mux.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/get-started", nil))
	page := r.Body.String()
	_, rest, _ := strings.Cut(page, `<details class="accordion" id="claude" name="pick-ai">`)
	card, _, _ := strings.Cut(rest, "</details>")
	if !strings.Contains(card, "Add custom connector") || !strings.Contains(card, "https://hack.example/mcp") {
		t.Errorf("self-hosted Hack Claude card lacks its own custom connector: %s", card)
	}
	for _, w := range []string{"Browse connectors", "<!--sh:", "in review in the ChatGPT app directory"} {
		if strings.Contains(page, w) {
			t.Errorf("self-hosted Hack Get started says %q", w)
		}
	}
}

// Every historical skills-only archive keeps its published address and its
// bytes: the checked-in list was recorded before the toolkit moved into the
// product (D3-S2b), and simple-hack.app serves each file unchanged.
func TestHackHistoricalArchivesKeepTheirBytes(t *testing.T) {
	list, err := os.ReadFile("../webassets/testdata/historical-archives.sha256")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, line := range strings.Split(string(list), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sum, name, ok := strings.Cut(line, "  ")
		if !ok || len(sum) != 64 {
			t.Fatalf("malformed line %q", line)
		}
		want[name] = sum
	}
	versions := []string{"0.2.6", "0.2.7", "0.2.8", "0.2.9", "0.2.10", "0.2.11", "0.2.12", "0.2.13"}
	if len(want) != len(versions) {
		t.Fatalf("the list names %d archives, want %d", len(want), len(versions))
	}
	embedded, err := fs.Glob(hackassets.HackFiles, "site/downloads/*.zip")
	if err != nil || len(embedded) != len(versions) {
		t.Fatalf("embedded archives: %v, %v", embedded, err)
	}
	sh := chromeTestHandlerWithBase(productedition.Hack, "https://simple-hack.app")
	mux := http.NewServeMux()
	hackhttp.RegisterHackGetStarted(mux, sh.Pages(), hackAssetsFor(sh))
	for _, version := range versions {
		name := "simple-hack-skills-only-" + version + ".zip"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/"+name, nil))
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/zip" || rec.Header().Get("Content-Disposition") != `attachment; filename="`+name+`"` {
			t.Fatalf("%s: %d %v", name, rec.Code, rec.Header())
		}
		sum := sha256.Sum256(rec.Body.Bytes())
		if got := hex.EncodeToString(sum[:]); got != want[name] {
			t.Errorf("%s: served SHA-256 %s, recorded %s", name, got, want[name])
		}
	}
}

// The platform's plugin archive and installers stay withdrawn on Hack, with
// the same answers as before Hack registered them itself.
func TestHackWithdrawnPlatformDownloads(t *testing.T) {
	sh := chromeTestHandler(productedition.Hack)
	mux := http.NewServeMux()
	hackhttp.RegisterHackGetStarted(mux, sh.Pages(), hackAssetsFor(sh))
	handler.RegisterUIRoutes(mux, "https://simple-hack.app", sh)
	for path, want := range map[string]string{
		"/plugin.zip":  "this legacy plugin download was withdrawn\n",
		"/install.sh":  "this installer was withdrawn\n",
		"/install.ps1": "this installer was withdrawn\n",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusGone || rec.Body.String() != want || rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Errorf("%s: %d %q %v", path, rec.Code, rec.Body.String(), rec.Header())
		}
	}
}

// /get-started names the hosted service's GitHub skills only on
// simple-hack.app; another install offers its own ZIP and its own sign-in
// and cost wording. The change is Hack's (it was a branch in the platform's
// page handler before D3-S2b).
func TestHackGetStartedOnAnotherInstall(t *testing.T) {
	page := func(base string) *httptest.ResponseRecorder {
		sh := chromeTestHandlerWithBase(productedition.Hack, base)
		mux := http.NewServeMux()
		hackhttp.RegisterHackGetStarted(mux, sh.Pages(), hackAssetsFor(sh))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/get-started", nil))
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/html; charset=utf-8" || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Content-Security-Policy") == "" {
			t.Fatalf("%s: %d %v", base, rec.Code, rec.Header())
		}
		return rec
	}
	hosted := page("https://simple-hack.app").Body.String()
	for _, want := range []string{"<li>Install the five Simple Hack skills", "npx -y skills add https://github.com/vineetu/simple-host/tree/main/products/hack/webassets/hack-toolkit", "with Google or an email code", "Yes. Running an event, joining, judging and hosting team sites are free."} {
		if !strings.Contains(hosted, want) {
			t.Errorf("simple-hack.app page lacks %q", want)
		}
	}
	other := page("https://hack.college.test").Body.String()
	for _, want := range []string{`<li>Download the <a href="/skills.zip" download>five Simple Hack skill folders</a>, install them in your agent’s skills directory, then restart your agent.</li>`, "with an email code or a configured sign-in provider", "Simple Hack is free software. Server, domain and email costs are your provider’s bill."} {
		if !strings.Contains(other, want) {
			t.Errorf("another install's page lacks %q", want)
		}
	}
	for _, gone := range []string{"<li>Install the five Simple Hack skills", "npx -y skills add", "with Google or an email code", "hosting team sites are free."} {
		if strings.Contains(other, gone) {
			t.Errorf("another install's page still says %q", gone)
		}
	}
	if strings.Contains(other, "<script>") || !strings.Contains(other, `<script nonce="`) {
		t.Error("another install's page has an inline script without the response nonce")
	}
}
