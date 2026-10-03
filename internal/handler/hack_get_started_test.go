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
	plugin "github.com/vsriram/simple-host/simple-host-website"
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
			want, err := plugin.FS.ReadFile("skills/" + f.Name)
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
	if page := get("/get-started"); page.Code != 200 || !strings.Contains(page.Body.String(), "/v1/skills/judge-hackathon/SKILL.md") || !strings.Contains(page.Body.String(), "/simple-hack-skills-only-0.2.4.zip") || !strings.Contains(page.Body.String(), "version 0.27.16") {
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
	for _, version := range []string{"0.2.0", "0.2.1", "0.2.2", "0.2.3", "0.2.4"} {
		name := "simple-hack-skills-only-" + version + ".zip"
		primary, err := hacktoolkit.Files.ReadFile("site/downloads/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if download := get("/" + name); download.Code != 200 || !bytes.Equal(download.Body.Bytes(), primary) {
			t.Fatalf("ChatGPT ZIP %s: %d", version, download.Code)
		}
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
