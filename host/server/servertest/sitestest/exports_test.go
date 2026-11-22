package sitestest

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites/export"
	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
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
	if names := fixture.TarNames(t, r.Body); !names["blog/files/index.html"] || !names["blog/files/_redirects"] {
		t.Errorf("archive holds %v", names)
	}
	for name, tok := range map[string]string{
		"expired":               export.SignExportToken(a.Sites.Handler, ann.ID, annSite, time.Now().Add(-time.Minute)),
		"another person's site": export.SignExportToken(a.Sites.Handler, ann.ID, bobSite, time.Now().Add(time.Minute)),
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
