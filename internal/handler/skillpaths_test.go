package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Any skill path that works under /v1/skills/ must work under /skills/ and the
// other way round: an agent fetched /v1/skills/connect-domain/SKILL.md and got
// a 404 because only /skills/… accepted the /SKILL.md suffix.
func TestSkillPathsWorkUnderBothPrefixes(t *testing.T) {
	mux := http.NewServeMux()
	RegisterSkillsHub(mux, "https://simple-host.app")
	RegisterUIRoutes(mux, "https://simple-host.app", chromeTestHandler())

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	dirs := bundledSkillDirs()
	if len(dirs) == 0 {
		t.Fatal("no bundled skills")
	}
	for _, dir := range dirs {
		for _, prefix := range []string{"/v1/skills/", "/skills/"} {
			for _, suffix := range []string{"", "/SKILL.md"} {
				path := prefix + dir + suffix
				rec := get(path)
				if rec.Code != http.StatusOK {
					t.Errorf("GET %s = %d, want 200", path, rec.Code)
					continue
				}
				if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
					t.Errorf("GET %s Content-Type = %q", path, ct)
				}
				if !strings.HasPrefix(rec.Body.String(), "---") {
					t.Errorf("GET %s did not return a SKILL.md", path)
				}
			}
		}
		for _, f := range skillFiles(dir) {
			if !strings.HasPrefix(f, "references/") {
				continue
			}
			for _, prefix := range []string{"/v1/skills/", "/skills/"} {
				path := prefix + dir + "/" + f
				if rec := get(path); rec.Code != http.StatusOK {
					t.Errorf("GET %s = %d, want 200", path, rec.Code)
				}
			}
		}
	}

	// The two /v1 forms are the same document.
	if a, b := get("/v1/skills/connect-domain").Body.String(), get("/v1/skills/connect-domain/SKILL.md").Body.String(); a != b {
		t.Error("/v1/skills/connect-domain and /v1/skills/connect-domain/SKILL.md differ")
	}
	for _, path := range []string{"/skills/connect-domain/references/registrars.md", "/v1/skills/connect-domain/references/registrars.md"} {
		if rec := get(path); rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
	}
	for _, path := range []string{"/v1/skills/no-such-skill/SKILL.md", "/skills/no-such-skill", "/skills/connect-domain/references/nope.md"} {
		if rec := get(path); rec.Code == http.StatusOK {
			t.Errorf("GET %s = 200, want an error", path)
		}
	}
	if rec := get("/skills.zip"); rec.Code != http.StatusOK {
		t.Errorf("GET /skills.zip = %d", rec.Code)
	}
}
