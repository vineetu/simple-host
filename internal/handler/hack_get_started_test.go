package handler

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	hacktoolkit "github.com/vsriram/simple-host/hack-toolkit"
)

func TestHackGetStartedSkills(t *testing.T) {
	previousMode := hackMode
	hackMode = true
	defer func() { hackMode = previousMode }()
	first, err := buildHackSkillsZip()
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildHackSkillsZip()
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
			want, err := hacktoolkit.Skills.ReadFile("skills/" + f.Name)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(data, skillServedText(f.Name, want)) {
				t.Fatalf("%s differs from served canonical source", f.Name)
			}
		}
	}
	want := map[string]bool{}
	for _, name := range hackSkillNames {
		want[name] = true
	}
	if !reflect.DeepEqual(tops, want) {
		t.Fatalf("ZIP skill folders %v, want %v", tops, want)
	}

	mux := http.NewServeMux()
	RegisterHackGetStarted(mux)
	RegisterSkillsHub(mux, "https://simple-hack.app")
	mux.HandleFunc("GET /skills.zip", serveSkillsZip)
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRecorder()
		mux.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		return r
	}
	if page := get("/get-started"); page.Code != 200 || !strings.Contains(page.Body.String(), "Run, join or judge a hackathon from the AI you already use.") {
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
	primary, err := hacktoolkit.Files.ReadFile("site/downloads/" + name)
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
	for _, name := range hackSkillNames {
		if raw := get("/v1/skills/" + name + "/SKILL.md"); raw.Code != 200 || !strings.Contains(raw.Body.String(), "name: "+name) {
			t.Fatalf("raw %s: %d", name, raw.Code)
		}
	}
}

func TestHackGetStartedOnboarding(t *testing.T) {
	previousMode := hackMode
	hackMode = true
	defer func() { hackMode = previousMode }()
	previousChrome := hackChrome
	SetHackChrome(true)
	t.Cleanup(func() { SetHackChrome(previousChrome) })
	mux := http.NewServeMux()
	RegisterHackGetStarted(mux)
	r := httptest.NewRecorder()
	mux.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/get-started", nil))
	page := r.Body.String()
	last := -1
	for _, heading := range []string{"Run, join or judge a hackathon from the AI you already use.", "Pick your AI", "Try one of these", "What happens next", "Other ways to install"} {
		at := strings.Index(page, heading)
		if at <= last {
			t.Fatalf("missing or out-of-order heading %q", heading)
		}
		last = at
	}
	for _, id := range []string{"chatgpt", "claude", "grok", "copilot", "coding-agents"} {
		_, rest, found := strings.Cut(page, `<details class="accordion" id="`+id+`" name="pick-ai">`)
		card, _, _ := strings.Cut(rest, "</details>")
		if !found || strings.Count(card, "<li>") != 3 || !strings.Contains(card, "https://simple-hack.app/mcp") || !strings.Contains(card, "Google or an email code") || !strings.Contains(card, "<b>Allow</b>") {
			t.Errorf("%s must have three steps, connector address and sign-in/consent instructions", id)
		}
	}
	_, other, _ := strings.Cut(page, `<details class="other" id="other-installs">`)
	other, _, _ = strings.Cut(other, "</details>")
	if other == "" || strings.Contains(other, "<summary open") || strings.Contains(page, `id="other-installs" open`) {
		t.Fatal("other installs must be collapsed")
	}
	for _, url := range []string{"/skills.zip", "/hack-skills.zip", "/simple-hack-skills-only-0.2.9.zip", "/v1/skills/run-hackathon/references/organiser-api.md", "/v1/skills/website-deploy/references/storage.md"} {
		if !strings.Contains(other, `href="`+url+`"`) {
			t.Errorf("download/reference %s missing from other installs", url)
		}
	}
	for _, name := range hackSkillNames {
		if !strings.Contains(other, `href="/v1/skills/`+name+`/SKILL.md"`) {
			t.Errorf("skill %s missing from other installs", name)
		}
	}
	for _, stale := range []string{"Choose a skill", "Read run-hackathon", "version 0.27", "prepared this package", "not been submitted"} {
		if strings.Contains(page, stale) {
			t.Errorf("stale onboarding copy %q", stale)
		}
	}
	for _, want := range []string{"team.event.simple-hack.app", "https://simple-host.app/hackathons", "ranks and overall scores are public by default", "After results are published", `id="prompt-organiser"`, `id="prompt-participant"`, `id="prompt-judge"`, `class="sh-header sh-hack"`, `class="sh-footer"`, "/hack-ink.css?v="} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Count(page, "data-copy-target=") != 9 {
		t.Fatal("expected five connector, one install and three prompt Copy buttons")
	}
	if !strings.Contains(page, `nonce="`) || strings.Contains(page, "<!--sh:") {
		t.Fatal("page must use the shared chrome and CSP nonce")
	}
}
