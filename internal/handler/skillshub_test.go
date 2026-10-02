package handler

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSkillsBundleExcludesControlPlaneSkills(t *testing.T) {
	// The bundle is what a participant installs. run-hackathon provisions cloud
	// servers with the operator's own credentials; it shipped to every
	// participant for months, and the Get Started page told them to expect
	// three folders while four arrived.
	data, err := buildSkillsZip()
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

func TestHostSkillsHideHostedMemberGuides(t *testing.T) {
	previousMode := hackMode
	hackMode = false
	defer func() { hackMode = previousMode }()
	mux := http.NewServeMux()
	RegisterSkillsHub(mux, "https://simple-host.app")
	RegisterUIRoutes(mux, "https://simple-host.app", chromeTestHandler())
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
	if catalog.Count != 4 || len(catalog.Skills) != 4 {
		t.Fatalf("host catalog has %d skills, want its existing four", catalog.Count)
	}
	for _, path := range []string{"/v1/skills/join-hackathon/SKILL.md", "/v1/skills/judge-hackathon/SKILL.md", "/skills/join-hackathon/SKILL.md", "/skills/judge-hackathon/SKILL.md"} {
		if rec := get(path); rec.Code != http.StatusNotFound {
			t.Errorf("host %s = %d, want 404", path, rec.Code)
		}
	}
	if rec := get("/v1/skills/run-hackathon/SKILL.md"); rec.Code != http.StatusOK {
		t.Errorf("existing self-host organiser guide = %d", rec.Code)
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
			if skill.Name == "join-hackathon" || skill.Name == "judge-hackathon" {
				t.Errorf("host discovery exposed %s", skill.Name)
			}
		}
	}
}
