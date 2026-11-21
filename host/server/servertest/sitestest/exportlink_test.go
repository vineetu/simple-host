package sitestest

import (
	"github.com/vsriram/simple-host/host/server/servertest"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The owner mints a link; anyone holding it downloads that one site's export
// with no key, until it expires or the site goes. Needs DB_DSN.
func TestExportLinkEndToEnd(t *testing.T) {
	a := servertest.NewPrivateApp(t)
	ann, bob := a.NewPerson(t, "ann"), a.NewPerson(t, "bob")
	const apex = "simple-host.test"
	a.Deploy(t, ann, "blog")
	a.Deploy(t, bob, "blog")
	if r := a.At(t, "PUT", apex, "/v1/sites/blog/files?keep=1", map[string]any{"files": map[string]string{"_redirects": "/before /after"}}, map[string]string{"X-API-Key": ann.Key}); r.Status != 200 {
		t.Fatal(r.Status, string(r.Body))
	}

	r := a.At(t, "POST", apex, "/v1/sites/blog/export-link", nil, map[string]string{"X-API-Key": ann.Key})
	if r.Status != http.StatusOK {
		t.Fatalf("mint: %d %s", r.Status, r.Body)
	}
	body := r.JSON(t)
	link, _ := body["url"].(string)
	if !strings.HasPrefix(link, "https://"+servertest.PcSiteDomain+"/v1/export?token=") || body["site"] != "blog" || body["expires_at"] == "" {
		t.Fatalf("mint answer: %s", r.Body)
	}
	u, _ := url.Parse(link)
	token := u.Query().Get("token")

	r = a.At(t, "GET", apex, "/v1/export?token="+url.QueryEscape(token), nil, nil)
	if r.Status != http.StatusOK || r.Header.Get("Content-Type") != "application/gzip" || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("download: %d %v %s", r.Status, r.Header, r.Body)
	}
	names := servertest.TarNames(t, r.Body)
	if !names["blog/files/index.html"] || !names["blog/files/_redirects"] {
		t.Errorf("archive holds %v", names)
	}
	annID, _ := a.UserID(t, ann)
	annSite := a.SiteID(t, ann, "blog")
	bobSite := a.SiteID(t, bob, "blog")

	for name, tok := range map[string]string{
		"expired":               a.Sites.SignExportToken(annID, annSite, time.Now().Add(-time.Minute)),
		"another person's site": a.Sites.SignExportToken(annID, bobSite, time.Now().Add(time.Minute)),
		"tampered":              flipMiddle(token),
		"missing":               "",
	} {
		r := a.At(t, "GET", apex, "/v1/export?token="+url.QueryEscape(tok), nil, nil)
		if r.Status != http.StatusNotFound || r.JSON(t)["code"] != "export_link_invalid" {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}

	// Minting needs the owner: no key, or someone else's site, is refused.
	if r := a.At(t, "POST", apex, "/v1/sites/blog/export-link", nil, nil); r.Status != http.StatusUnauthorized {
		t.Errorf("mint without a key: %d", r.Status)
	}
	if r := a.At(t, "POST", apex, "/v1/sites/nosuch/export-link", nil, map[string]string{"X-API-Key": ann.Key}); r.Status != http.StatusNotFound {
		t.Errorf("mint for a missing site: %d", r.Status)
	}

	// The link dies with the site.
	if r := a.At(t, "DELETE", apex, "/v1/sites/blog", nil, map[string]string{"X-API-Key": ann.Key}); r.Status/100 != 2 {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", apex, "/v1/export?token="+url.QueryEscape(token), nil, nil); r.Status != http.StatusNotFound {
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
