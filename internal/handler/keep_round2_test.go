package handler

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/vsriram/simple-host/internal/db"
)

func TestKeepSanitizedPatch(t *testing.T) {
	for _, tc := range []struct {
		name, wantIndex string
		patch           map[string]string
	}{
		{"ignored credentials", "patched", map[string]string{"index.html": "patched", ".git/hooks/post-checkout.sh": "ignored", ".env": "ignored"}},
		{"AppleDouble", "patched", map[string]string{"index.html": "patched", "._legacy.php": "ignored"}},
		{"metadata only", "version two", map[string]string{".DS_Store": "ignored"}},
		{"empty patch", "version two", map[string]string{}},
		{"normalized patch", "patched", map[string]string{"./index.html": "patched", "._legacy.php": "ignored"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, user := keepReviewSite(t)
			// Inspect the pre-lock result so discarded entries cannot affect merge caps.
			raw, remove, _, _, err := a.sites.decodeJSONFiles(httptest.NewRecorder(), keepReviewRequest(user, jsonBody(map[string]any{"files": tc.patch})))
			if err != nil || len(remove) != 0 {
				t.Fatal(err, remove)
			}
			for name := range raw {
				if name != "index.html" {
					t.Fatal("unsanitized patch entry:", name)
				}
			}
			w := httptest.NewRecorder()
			a.sites.updateSiteFiles(w, keepReviewRequest(user, jsonBody(map[string]any{"files": tc.patch})))
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			site, err := db.GetSiteByUser(context.Background(), a.database, user.ID, "blog")
			if err != nil {
				t.Fatal(err)
			}
			dir := a.sites.disk.VersionDir(user.ID, "blog", site.ActiveVersion)
			for name, want := range map[string]string{"index.html": tc.wantIndex, "about.html": "version two"} {
				body, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil || string(body) != want {
					t.Fatal(name, string(body), err)
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 2 {
				t.Fatal("discarded files reached merged version:", entries, err)
			}
		})
	}
}
