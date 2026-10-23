package handler

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/buildinfo"
	"golang.org/x/net/html"
)

func TestHomepageDiscovery(t *testing.T) {
	mux := chromeTestMux(t)
	var markdown string
	for _, accept := range []string{"text/markdown", "text/html", "", "text/html;q=0.5, text/markdown", "text/markdown;q=0, text/html", "text/markdown;q=0.5, text/html"} {
		req := httptest.NewRequest(http.MethodGet, "https://simple-host.app/", nil)
		req.Header.Set("Accept", accept)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Header().Get("Vary") != "Accept" {
			t.Fatalf("Accept %q: status %d, Vary %q", accept, rec.Code, rec.Header().Get("Vary"))
		}
		for _, link := range []string{`</sitemap.xml>; rel="sitemap"`, `</index.md>; rel="alternate"; type="text/markdown"`, `</.well-known/api-catalog>; rel="api-catalog"`, `</openapi.json>; rel="service-desc"`} {
			if !strings.Contains(rec.Header().Get("Link"), link) {
				t.Errorf("Accept %q: Link missing %s", accept, link)
			}
		}
		if accept == "text/markdown" || accept == "text/html;q=0.5, text/markdown" {
			if rec.Header().Get("Content-Type") != "text/markdown; charset=utf-8" || !strings.HasPrefix(rec.Body.String(), "---\ntitle:") || !strings.Contains(rec.Body.String(), "# Describe a website.") || !strings.Contains(rec.Body.String(), "[Get started](/install.html)") || strings.Contains(rec.Body.String(), "<script") {
				t.Fatalf("unexpected markdown response: %v %s", rec.Header(), rec.Body.String())
			}
			markdown = rec.Body.String()
		} else if rec.Header().Get("Content-Type") != "text/html; charset=utf-8" || !strings.Contains(rec.Body.String(), `<link rel="alternate" type="text/markdown" href="/index.md">`) {
			t.Fatalf("Accept %q: HTML/alternate missing", accept)
		}
	}
	for _, path := range []string{"/index.md", "/llms.md"} {
		rec := get(t, mux, "simple-host.app", path)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/markdown; charset=utf-8" || rec.Body.String() != markdown {
			t.Errorf("%s: markdown differs from /: %d %v %s", path, rec.Code, rec.Header(), rec.Body.String())
		}
	}
	// These query strings must keep their existing redirect, even for agents.
	for _, query := range []string{"token=t", "cn=c", "new=1", "job=j"} {
		req := httptest.NewRequest(http.MethodGet, "/?"+query, nil)
		req.Header.Set("Accept", "text/markdown")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/dashboard?"+query {
			t.Errorf("%s: redirect changed: %d %v", query, rec.Code, rec.Header())
		}
	}
	for _, accept := range []string{"text/markdown", "text/html"} {
		req := httptest.NewRequest(http.MethodGet, "/missing-page.html", nil)
		req.Header.Set("Accept", accept)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound || rec.Header().Get("Vary") != "Accept" || rec.Header().Get("Content-Type") != accept+"; charset=utf-8" {
			t.Errorf("404 Accept %s: %d %v", accept, rec.Code, rec.Header())
		}
		if accept == "text/markdown" && !strings.HasPrefix(rec.Body.String(), "# 404") {
			t.Error("missing markdown 404 body")
		}
	}
}

func TestDiscoveryDocuments(t *testing.T) {
	mux := chromeTestMux(t)
	rec := get(t, mux, "simple-host.app", "/.well-known/ard.json")
	var ard struct {
		SpecVersion string `json:"specVersion"`
		Host        struct {
			Identifier string `json:"identifier"`
		} `json:"host"`
		Entries []struct {
			Identifier string `json:"identifier"`
			Type       string `json:"type"`
			URL        string `json:"url"`
		} `json:"entries"`
	}
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" || json.Unmarshal(rec.Body.Bytes(), &ard) != nil {
		t.Fatalf("ARD: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	// identifier -> url: ARD v0.91 entries (urn:air identifiers, not a bare type).
	want := map[string]string{
		"urn:air:simple-host.app:server:mcp":           "https://simple-host.app/.well-known/mcp/server-card.json",
		"urn:air:simple-host.app:skill:website-deploy": "https://simple-host.app/.well-known/skills/index.json",
		"urn:air:simple-host.app:api:rest":             "https://simple-host.app/openapi.json",
	}
	for _, entry := range ard.Entries {
		if url, ok := want[entry.Identifier]; !ok || url != entry.URL || entry.Type == "" {
			t.Errorf("unexpected ARD entry: %+v", entry)
		}
		delete(want, entry.Identifier)
	}
	if ard.SpecVersion != "1.0" || ard.Host.Identifier == "" || len(want) != 0 {
		t.Errorf("ARD specVersion %s, host %+v, missing entries %v", ard.SpecVersion, ard.Host, want)
	}
	rec = get(t, mux, "simple-host.app", "/.well-known/api-catalog")
	var catalog struct {
		Linkset []map[string]json.RawMessage `json:"linkset"`
	}
	wantCatalogType := `application/linkset+json;profile="https://www.rfc-editor.org/info/rfc9727"`
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != wantCatalogType || json.Unmarshal(rec.Body.Bytes(), &catalog) != nil || len(catalog.Linkset) != 1 {
		t.Fatalf("catalog: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	for rel, links := range map[string]map[string]string{
		"item":         {"openapi.json": "application/vnd.oai.openapi+json"},
		"service-desc": {"openapi.json": "application/vnd.oai.openapi+json", "openapi.yaml": "application/yaml"},
		"service-doc":  {"llms.txt": "text/plain", "docs.html": "text/html"},
	} {
		var entries []struct{ Href, Type string }
		if err := json.Unmarshal(catalog.Linkset[0][rel], &entries); err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			file := strings.TrimPrefix(entry.Href, "https://simple-host.app/")
			if links[file] != entry.Type {
				t.Errorf("unexpected %s entry: %+v", rel, entry)
			}
			delete(links, file)
		}
		if len(links) != 0 {
			t.Errorf("missing %s links: %v", rel, links)
		}
	}
}

func TestHomepageCopyAndStructuredData(t *testing.T) {
	// Editing the HTML changes markdown directly; no second copy to update.
	page, _ := staticFiles.ReadFile("static/host-story.html")
	page = bytes.ReplaceAll(page, []byte("Ask the AI you already use. Free."), []byte("Updated copy &amp; details."))
	md, err := homepageMarkdown(page)
	if err != nil || !bytes.Contains(md, []byte("Updated copy & details.")) || bytes.Contains(md, []byte("Ask the AI you already use.")) {
		t.Fatalf("markdown did not follow HTML: %s, %v", md, err)
	}
	for _, name := range []string{"index.html", "host-story.html"} {
		page, _ := staticFiles.ReadFile("static/" + name)
		tokens := html.NewTokenizer(bytes.NewReader(page))
		var description, schema string
		for tokens.Next() != html.ErrorToken {
			token := tokens.Token()
			attrs := map[string]string{}
			for _, a := range token.Attr {
				attrs[a.Key] = a.Val
			}
			if token.Data == "meta" && attrs["name"] == "description" {
				description = attrs["content"]
			}
			if token.Data == "script" && attrs["type"] == "application/ld+json" {
				tokens.Next()
				schema = string(tokens.Text())
			}
		}
		var data struct {
			Graph []map[string]any `json:"@graph"`
		}
		if err := json.Unmarshal([]byte(schema), &data); err != nil || len(data.Graph) != 2 {
			t.Fatalf("%s: invalid JSON-LD: %v", name, err)
		}
		if data.Graph[0]["@type"] != "SoftwareApplication" || data.Graph[0]["description"] != description || data.Graph[1]["@type"] != "Organization" {
			t.Errorf("%s: wrong schema nodes or description", name)
		}
	}
}

func TestSitemapBuildLastmod(t *testing.T) {
	previous := buildinfo.BuildTime
	buildinfo.BuildTime = "2026-10-23T09:12:34Z"
	t.Cleanup(func() { buildinfo.BuildTime = previous })
	for _, host := range []string{"simple-host.app", "simple-hack.app"} {
		rec := get(t, http.HandlerFunc(serveSitemapXML), host, "/sitemap.xml")
		var parsed struct {
			URLs []struct {
				Loc     string `xml:"loc"`
				Lastmod string `xml:"lastmod"`
			} `xml:"url"`
		}
		if err := xml.Unmarshal(rec.Body.Bytes(), &parsed); err != nil || len(parsed.URLs) == 0 {
			t.Fatalf("invalid sitemap: %v", err)
		}
		for _, entry := range parsed.URLs {
			if entry.Lastmod != buildinfo.BuildTime {
				t.Errorf("%s: lastmod %q", entry.Loc, entry.Lastmod)
			}
		}
	}
}
