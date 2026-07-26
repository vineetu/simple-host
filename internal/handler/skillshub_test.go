package handler

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestBundledSkillDirsIncludesEveryShippedSkill(t *testing.T) {
	dirs := bundledSkillDirs()
	for _, want := range []string{"connect-domain", "website-deploy", "website-deploy-builder"} {
		if !slices.Contains(dirs, want) {
			t.Errorf("bundledSkillDirs() = %v, missing %q", dirs, want)
		}
	}
	if !slices.IsSorted(dirs) {
		t.Errorf("bundledSkillDirs() = %v, want sorted", dirs)
	}
}

// RegisterUIRoutes generates the per-skill download routes from bundledSkillDirs.
// connect-domain once shipped without them, so /skills/connect-domain.zip 404'd
// while the install page advertised the skill. Assert every bundled skill serves.
func TestEverySkillServesItsDownloads(t *testing.T) {
	for _, dir := range bundledSkillDirs() {
		t.Run(dir, func(t *testing.T) {
			for name, h := range map[string]http.HandlerFunc{
				"zip":      serveSkillZip(dir),
				"markdown": serveSkillMarkdown(dir),
			} {
				rec := httptest.NewRecorder()
				h(rec, httptest.NewRequest(http.MethodGet, "/skills/"+dir, nil))
				if rec.Code != http.StatusOK {
					t.Errorf("%s handler for %s = %d, want 200", name, dir, rec.Code)
				}
				if rec.Body.Len() == 0 {
					t.Errorf("%s handler for %s returned an empty body", name, dir)
				}
			}
		})
	}
}

func TestSkillFilesListsReferences(t *testing.T) {
	files := skillFiles("website-deploy")
	if !slices.Contains(files, "SKILL.md") {
		t.Fatalf("skillFiles(website-deploy) = %v, missing SKILL.md", files)
	}
	var refs int
	for _, f := range files {
		if strings.HasPrefix(f, "references/") {
			refs++
		}
	}
	if refs == 0 {
		t.Errorf("skillFiles(website-deploy) = %v, expected reference documents", files)
	}
	if !slices.IsSorted(files) {
		t.Errorf("skillFiles(website-deploy) = %v, want sorted", files)
	}
}

func TestServeSkillReference(t *testing.T) {
	mux := http.NewServeMux()
	RegisterSkillsHub(mux, "https://example.test")

	tests := []struct {
		name string
		path string
		want int
	}{
		{"a real reference", "/v1/skills/website-deploy/references/backend.md", http.StatusOK},
		{"unknown reference", "/v1/skills/website-deploy/references/nope.md", http.StatusNotFound},
		{"unknown skill", "/v1/skills/no-such-skill/references/backend.md", http.StatusNotFound},
		{"non-markdown is rejected", "/v1/skills/website-deploy/references/backend.txt", http.StatusBadRequest},
		{"uppercase is rejected", "/v1/skills/website-deploy/references/Backend.md", http.StatusBadRequest},
		{"well-known mirror works", "/.well-known/skills/website-deploy/references/backend.md", http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != tc.want {
				t.Errorf("GET %s = %d, want %d", tc.path, rec.Code, tc.want)
			}
			if tc.want == http.StatusOK && rec.Body.Len() == 0 {
				t.Errorf("GET %s returned an empty body", tc.path)
			}
		})
	}
}

// SKILL.md routes to references/*.md. A discovery hub that fetches only the
// files this index advertises must get the references too, or it hands the agent
// a table of contents with no chapters.
func TestWellKnownIndexAdvertisesReferences(t *testing.T) {
	mux := http.NewServeMux()
	RegisterSkillsHub(mux, "https://example.test")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/skills/index.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("index.json = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"website-deploy", "connect-domain", "references/backend.md"} {
		if !strings.Contains(body, want) {
			t.Errorf("index.json does not mention %q; got %s", want, body)
		}
	}
}

// Every human-facing doc cites the bare /skills/ prefix, so someone reading
// /skills/website-deploy/SKILL.md will guess /skills/website-deploy/references/
// for the files it names. That guess must work, not fall through to the static
// file server.
func TestReferencesServeUnderTheBareSkillsPrefix(t *testing.T) {
	for _, dir := range bundledSkillDirs() {
		for _, f := range skillFiles(dir) {
			if !strings.HasPrefix(f, "references/") {
				continue
			}
			file := strings.TrimPrefix(f, "references/")
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/skills/"+dir+"/"+f, nil)
			req.SetPathValue("file", file)
			serveSkillReferenceFor(dir)(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("/skills/%s/%s = %d, want 200", dir, f, rec.Code)
			}
		}
	}
}

// The bound-name handler must still reject a bad filename — binding the skill
// name from the route removes one guard, not both.
func TestBareSkillsPrefixRejectsBadFilenames(t *testing.T) {
	for _, file := range []string{"backend.txt", "Backend.md", "../SKILL.md", ".md", ""} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/skills/website-deploy/references/x", nil)
		req.SetPathValue("file", file)
		serveSkillReferenceFor("website-deploy")(rec, req)
		if rec.Code == http.StatusOK {
			t.Errorf("file %q was served, want a rejection", file)
		}
	}
}

// Every reference the router SKILL.md points at must actually exist — a broken
// pointer is worse than the monolith it replaced.
func TestSkillReferencePointersResolve(t *testing.T) {
	for _, dir := range bundledSkillDirs() {
		files := skillFiles(dir)
		for _, f := range files {
			if !strings.HasPrefix(f, "references/") {
				continue
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/skills/"+dir+"/"+f, nil)
			req.SetPathValue("name", dir)
			req.SetPathValue("file", strings.TrimPrefix(f, "references/"))
			serveSkillReference(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("%s/%s served %d, want 200", dir, f, rec.Code)
			}
		}
	}
}
