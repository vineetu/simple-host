package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sitesrepo "github.com/vsriram/simple-host/host/repository/sites"
)

func TestKeepUnreadableRegularFile(t *testing.T) {
	for _, inaccessible := range []string{"bytes", "metadata"} {
		for _, action := range []string{"remove", "replace", "retain"} {
			t.Run(inaccessible+"/"+action, func(t *testing.T) {
				a, user := keepReviewSite(t)
				path := filepath.Join(a.sites.disk.VersionDir(user.ID, "blog", 2), "about.html")
				target, mode, restoreMode := path, os.FileMode(0000), os.FileMode(0644)
				if inaccessible == "metadata" {
					// Listing succeeds, but looking up the children's metadata fails.
					target, mode, restoreMode = filepath.Dir(path), 0444, 0755
				}
				if err := os.Chmod(target, mode); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(target, restoreMode) })
				if inaccessible == "metadata" {
					if _, err := os.ReadDir(target); err != nil {
						t.Fatalf("list retained directory: %v", err)
					}
					if _, err := os.Stat(path); err == nil {
						t.Skip("directory permissions do not restrict this user")
					} else if !os.IsPermission(err) {
						t.Fatal(err)
					}
				}
				if _, err := os.ReadFile(path); err == nil {
					t.Skip("file permissions do not restrict this user")
				} else if !os.IsPermission(err) {
					t.Fatal(err)
				}
				patch := map[string]string{"index.html": "patched"}
				body := map[string]any{"files": patch}
				switch action {
				case "remove":
					body["remove_files"] = []string{"about.html"}
				case "replace":
					patch["about.html"] = "replacement"
				}
				w := httptest.NewRecorder()
				a.sites.updateSiteFiles(w, keepReviewRequest(user, jsonBody(body)))
				if action == "retain" {
					var got errorResponse
					if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
						t.Fatal(err)
					}
					if w.Code != 400 || !strings.Contains(got.Error, os.ErrPermission.Error()) {
						t.Fatalf("unreadable retained bytes: %d %s", w.Code, w.Body.String())
					}
					return
				}
				if w.Code != 200 {
					t.Fatalf("%s unreadable file: %d %s", action, w.Code, w.Body.String())
				}
				dir := a.sites.disk.VersionDir(user.ID, "blog", 3)
				index, err := os.ReadFile(filepath.Join(dir, "index.html"))
				if err != nil || string(index) != "patched" {
					t.Fatalf("patch contents: %q %v", index, err)
				}
				about, err := os.ReadFile(filepath.Join(dir, "about.html"))
				if action == "remove" {
					if !os.IsNotExist(err) {
						t.Fatalf("removed file: %q %v", about, err)
					}
				} else if err != nil || string(about) != "replacement" {
					t.Fatalf("replacement: %q %v", about, err)
				}
			})
		}
	}
}

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
			site, err := sitesrepo.GetSiteByUser(context.Background(), a.database, user.ID, "blog")
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
