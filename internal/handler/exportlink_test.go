package handler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestExportTokenRefusesWhatItShould(t *testing.T) {
	h := &SiteHandler{exportKey: newExportKey()}
	now := time.Now()
	good := h.signExportToken("user-1", "site-1", now.Add(exportLinkTTL))
	if u, s, err := h.checkExportToken(good, now); err != nil || u != "user-1" || s != "site-1" {
		t.Fatalf("good token refused: %v %q %q", err, u, s)
	}
	enc, mac, _ := strings.Cut(good, ".")
	swapped := base64.RawURLEncoding.EncodeToString([]byte("user-1.site-2."+strings.Split(mustDecode(t, enc), ".")[2])) + "." + mac
	other := &SiteHandler{exportKey: newExportKey()}
	for name, tok := range map[string]string{
		"expired":           h.signExportToken("user-1", "site-1", now.Add(-time.Second)),
		"expires too late":  h.signExportToken("user-1", "site-1", now.Add(exportLinkTTL+time.Minute)),
		"other site":        swapped,
		"flipped signature": enc + "." + strings.Repeat("A", len(mac)),
		"another key":       other.signExportToken("user-1", "site-1", now.Add(time.Minute)),
		"no signature":      enc,
		"empty":             "",
		"garbage":           "!!!.???",
	} {
		if _, _, err := h.checkExportToken(tok, now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func mustDecode(t *testing.T, s string) string {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The owner mints a link; anyone holding it downloads that one site's export
// with no key, until it expires or the site goes. Needs DB_DSN.
func TestExportLinkEndToEnd(t *testing.T) {
	a := newPrivateApp(t)
	ann, bob := a.newPerson(t, "ann"), a.newPerson(t, "bob")
	const apex = "simple-host.test"
	a.deploy(t, ann, "blog")
	a.deploy(t, bob, "blog")

	r := a.at(t, "POST", apex, "/v1/sites/blog/export-link", nil, map[string]string{"X-API-Key": ann.key})
	if r.status != http.StatusOK {
		t.Fatalf("mint: %d %s", r.status, r.body)
	}
	body := r.json(t)
	link, _ := body["url"].(string)
	if !strings.HasPrefix(link, "https://"+pcSiteDomain+"/v1/export?token=") || body["site"] != "blog" || body["expires_at"] == "" {
		t.Fatalf("mint answer: %s", r.body)
	}
	u, _ := url.Parse(link)
	token := u.Query().Get("token")

	r = a.at(t, "GET", apex, "/v1/export?token="+url.QueryEscape(token), nil, nil)
	if r.status != http.StatusOK || r.header.Get("Content-Type") != "application/gzip" || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("download: %d %v %s", r.status, r.header, r.body)
	}
	names := tarNames(t, r.body)
	if !names["blog/state.json"] || !names["blog/files/index.html"] {
		t.Errorf("archive holds %v", names)
	}
	annID, _ := a.userID(t, ann)
	annSite := a.siteID(t, ann, "blog")
	bobSite := a.siteID(t, bob, "blog")

	for name, tok := range map[string]string{
		"expired":               a.sites.signExportToken(annID, annSite, time.Now().Add(-time.Minute)),
		"another person's site": a.sites.signExportToken(annID, bobSite, time.Now().Add(time.Minute)),
		"tampered":              token[:len(token)-2] + "xx",
		"missing":               "",
	} {
		r := a.at(t, "GET", apex, "/v1/export?token="+url.QueryEscape(tok), nil, nil)
		if r.status != http.StatusNotFound || r.json(t)["code"] != "export_link_invalid" {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}

	// Minting needs the owner: no key, or someone else's site, is refused.
	if r := a.at(t, "POST", apex, "/v1/sites/blog/export-link", nil, nil); r.status != http.StatusUnauthorized {
		t.Errorf("mint without a key: %d", r.status)
	}
	if r := a.at(t, "POST", apex, "/v1/sites/nosuch/export-link", nil, map[string]string{"X-API-Key": ann.key}); r.status != http.StatusNotFound {
		t.Errorf("mint for a missing site: %d", r.status)
	}

	// The link dies with the site.
	if r := a.at(t, "DELETE", apex, "/v1/sites/blog", nil, map[string]string{"X-API-Key": ann.key}); r.status/100 != 2 {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", apex, "/v1/export?token="+url.QueryEscape(token), nil, nil); r.status != http.StatusNotFound {
		t.Errorf("link for a deleted site: %d", r.status)
	}
}

func tarNames(t *testing.T, b []byte) map[string]bool {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	names := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names[hdr.Name] = true
	}
	return names
}
