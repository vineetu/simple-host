package sitestest

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites"
)

// The owner mints a link; anyone holding it downloads that one site's export
// with no key, until it expires or the site goes.
func TestExportLinkEndToEnd(t *testing.T) {
	a := NewApp(t)
	ann, bob := a.NewPerson(t, "ann"), a.NewPerson(t, "bob")
	annSite := a.Deploy(t, ann, "blog")
	bobSite := a.Deploy(t, bob, "blog")
	if r := a.api(t, ann, "PUT", "/v1/sites/blog/files?keep=1", map[string]any{"files": map[string]string{"_redirects": "/before /after"}}); r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}
	r := a.api(t, ann, "POST", "/v1/sites/blog/export-link", nil)
	link, _ := r.JSON(t)["url"].(string)
	if r.Status != http.StatusOK || !strings.HasPrefix(link, "https://"+rt.SiteDomain+"/v1/export?token=") {
		t.Fatalf("mint: %d %s", r.Status, r.Body)
	}
	u, _ := url.Parse(link)
	token := u.Query().Get("token")
	get := func(tok string) rt.Resp {
		return a.At(t, "GET", rt.SiteDomain, "/v1/export?token="+url.QueryEscape(tok), nil, nil)
	}

	r = get(token)
	if r.Status != http.StatusOK || r.Header.Get("Content-Type") != "application/gzip" || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("download: %d %v %s", r.Status, r.Header, r.Body)
	}
	if names := tarNames(t, r.Body); !names["blog/files/index.html"] || !names["blog/files/_redirects"] {
		t.Errorf("archive holds %v", names)
	}
	for name, tok := range map[string]string{
		"expired":               a.Sites.SignExportToken(ann.ID, annSite, time.Now().Add(-time.Minute)),
		"another person's site": a.Sites.SignExportToken(ann.ID, bobSite, time.Now().Add(time.Minute)),
		"tampered":              flipMiddle(token),
	} {
		if r := get(tok); r.Status != http.StatusNotFound || r.Code() != "export_link_invalid" {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	if r := a.At(t, "POST", rt.SiteDomain, "/v1/sites/blog/export-link", nil, nil); r.Status != http.StatusUnauthorized {
		t.Errorf("mint without a key: %d", r.Status)
	}
	// The link dies with the site.
	if r := a.api(t, ann, "DELETE", "/v1/sites/blog", nil); r.Status/100 != 2 {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if r := get(token); r.Status != http.StatusNotFound {
		t.Errorf("link for a deleted site: %d", r.Status)
	}
}

// flipMiddle changes one character in the middle of a token, well inside the
// ciphertext, so the change can never land on bits the encoding ignores.
func flipMiddle(token string) string {
	b := []byte(token)
	i := len(b) / 2
	if b[i] == 'A' {
		b[i] = 'B'
	} else {
		b[i] = 'A'
	}
	return string(b)
}

// An export still completes when part of the tree cannot be read: an
// unlistable live tree still exports its redirect rules, and an unreadable
// file in a deleted site's tree is skipped.
func TestExportSurvivesUnreadableFiles(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "export-review")
	archive := func(siteID string, files sites.FileVersion) map[string]string {
		t.Helper()
		contents := map[string]string{}
		err := a.Sites.LifecycleSvc().WriteArchive(context.Background(), func(name string, _ time.Time, _ int64, r io.Reader) error {
			b, err := io.ReadAll(r)
			contents[name] = string(b)
			return err
		}, files.SiteName, siteID, files)
		if err != nil {
			t.Fatal(err)
		}
		return contents
	}

	liveID := a.Deploy(t, owner, "live")
	dir := a.siteDir(owner, "live")
	const rules = "/old /new 302\n"
	if err := os.WriteFile(filepath.Join(dir, "redirects"), []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(dir, "current")
	if err := os.Chmod(current, 0o111); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(current, 0o755) })
	if _, err := os.ReadDir(current); err == nil {
		t.Skip("directory permissions do not restrict this user")
	}
	if got := archive(liveID, sites.FileVersion{UserID: owner.ID, SiteName: "live"}); !reflect.DeepEqual(got, map[string]string{"live/files/_redirects": rules}) {
		t.Fatalf("unlistable tree: %v", got)
	}

	trashID := a.Deploy(t, owner, "gone")
	if err := a.Disk.TrashSite(owner.ID, "gone", trashID); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.Disk.TrashDir(owner.ID, trashID), "current", "unreadable.txt")
	if err := os.WriteFile(path, []byte("unreadable bytes"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	got := archive(trashID, sites.FileVersion{UserID: owner.ID, SiteName: "gone", TrashID: trashID})
	if len(got) != 2 || got["gone/files/index.html"] == "" || got["gone/files/sub/index.html"] != "sub" {
		t.Fatalf("unreadable file not skipped: %v", got)
	}
}
