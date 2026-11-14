package handler

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/edition"
)

func TestSkillsBundleExcludesControlPlaneSkills(t *testing.T) {
	// The Simple Host bundle contains only its three website skills.
	data, err := newAssetPipeline(edition.ForKind(edition.Host), "").buildSkillsZip()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	tops := map[string]bool{}
	for _, f := range reader.File {
		if i := strings.Index(f.Name, "/"); i > 0 {
			tops[f.Name[:i]] = true
		}
	}
	for _, name := range controlPlaneSkills {
		if tops[name] {
			t.Errorf("%q is in the participant bundle", name)
		}
	}
	// And the ones that belong there still do, or the fix broke the product.
	for _, name := range []string{"website-deploy", "website-deploy-builder", "connect-domain"} {
		if !tops[name] {
			t.Errorf("%q is missing from the bundle", name)
		}
	}
}

func TestHostSkillsExcludeHackathonGuides(t *testing.T) {
	mux := http.NewServeMux()
	newAssetPipeline(edition.ForKind(edition.Host), "").registerSkillsHub(mux, "https://simple-host.app")
	RegisterUIRoutes(mux, "https://simple-host.app", chromeTestHandler(edition.Host))
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	var catalog struct {
		Count  int `json:"count"`
		Skills []struct {
			Name string `json:"name"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(get("/v1/skills").Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.Count != 3 || len(catalog.Skills) != 3 {
		t.Fatalf("host catalog has %d skills, want its three website skills", catalog.Count)
	}
	for _, skill := range []string{"join-hackathon", "judge-hackathon", "run-hackathon"} {
		for _, path := range []string{
			"/v1/skills/" + skill, "/v1/skills/" + skill + "/SKILL.md",
			"/skills/" + skill, "/skills/" + skill + "/SKILL.md", "/skills/" + skill + ".zip",
			"/.well-known/skills/" + skill + "/SKILL.md",
			"/v1/skills/" + skill + "/references/organiser-api.md",
			"/skills/" + skill + "/references/organiser-api.md",
		} {
			if rec := get(path); rec.Code != http.StatusNotFound {
				t.Errorf("host %s = %d, want 404", path, rec.Code)
			}
		}
	}
	if rec := get("/.well-known/skills/index.json"); rec.Code != http.StatusOK {
		t.Errorf("host discovery = %d", rec.Code)
	} else {
		var discovery struct {
			Skills []struct {
				Name string `json:"name"`
			} `json:"skills"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &discovery); err != nil {
			t.Fatal(err)
		}
		for _, skill := range discovery.Skills {
			if strings.HasSuffix(skill.Name, "-hackathon") {
				t.Errorf("host discovery exposed %s", skill.Name)
			}
		}
	}
}

func TestHostMissingSkillResponses(t *testing.T) {
	mux := http.NewServeMux()
	newAssetPipeline(edition.ForKind(edition.Host), "").registerSkillsHub(mux, "https://simple-host.app")
	for _, tc := range []struct {
		path    string
		message string
	}{
		{"/v1/skills/no-such-skill", "skill not found"},
		{"/v1/skills/no-such-skill/SKILL.md", "skill not found"},
		{"/v1/skills/no-such-skill/references/missing.md", "reference not found"},
		{"/v1/skills/website-deploy/references/missing.md", "reference not found"},
		{"/.well-known/skills/no-such-skill/SKILL.md", "skill not found"},
		{"/.well-known/skills/no-such-skill/references/missing.md", "reference not found"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
			if strings.HasPrefix(tc.path, "/v1/") {
				var body errorResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Error != tc.message || body.Code != "not_found" || body.Hint != "Use GET /v1/skills to find available skills and their references." {
					t.Errorf("unexpected error body: %s", rec.Body.String())
				}
			} else if rec.Body.String() != tc.message+"\n" {
				t.Errorf("body = %q, want %q", rec.Body.String(), tc.message+"\n")
			}
		})
	}
}
