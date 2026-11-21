package siteshttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"

	"github.com/vsriram/simple-host/host/edition"
)

func TestHostSkillsExcludeProductGuides(t *testing.T) {
	mux := http.NewServeMux()
	testAssetPipeline(edition.Default(config.DefaultLimits()), "").RegisterSkillsHub(mux, "https://simple-host.app")
	RegisterUIRoutes(mux, "https://simple-host.app", chromeTestHandler(platformTestEdition()))
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
