package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/httpx"

	"github.com/vsriram/simple-host/host/edition"
)

func TestSkillsBundleExcludesControlPlaneSkills(t *testing.T) {
	// The Simple Host bundle contains only its three website skills.
	data, err := testAssetPipeline(edition.Default(config.DefaultLimits()), "").BuildSkillsZip()
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

func TestHostMissingSkillResponses(t *testing.T) {
	mux := http.NewServeMux()
	testAssetPipeline(edition.Default(config.DefaultLimits()), "").RegisterSkillsHub(mux, "https://simple-host.app")
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
				var body httpx.ErrorResponse
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
