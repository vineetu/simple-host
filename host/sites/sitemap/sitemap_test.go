package sitemap

import (
	"context"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/storage"
)

func TestSitemapShards(t *testing.T) {
	old := sitemapURLLimit
	sitemapURLLimit = 2
	t.Cleanup(func() { sitemapURLLimit = old })
	pages := []SitemapURL{{Loc: "https://example.org/?x=1&y=2"}}
	sites := []SitemapURL{{Loc: "https://one.example.org/", Lastmod: "2026-11-12T00:00:00Z"}, {Loc: "https://two.example.org/"}, {Loc: "https://three.example.org/"}}
	files := buildSitemaps("https://example.org", pages, sites)
	var index struct {
		Maps []struct {
			Loc string `xml:"loc"`
		} `xml:"sitemap"`
	}
	if err := xml.Unmarshal(files["/sitemap.xml"], &index); err != nil || len(index.Maps) != 3 {
		t.Fatalf("index: %s (%v)", files["/sitemap.xml"], err)
	}
	for path, want := range map[string]int{"/sitemap-pages.xml": 1, "/sitemap-sites-1.xml": 2, "/sitemap-sites-2.xml": 1} {
		var set struct {
			URLs []SitemapURL `xml:"url"`
		}
		if err := xml.Unmarshal(files[path], &set); err != nil || len(set.URLs) != want {
			t.Fatalf("%s: %s (%v)", path, files[path], err)
		}
	}
	files = buildSitemaps("https://example.org", pages, sites[:1])
	if !strings.Contains(string(files["/sitemap.xml"]), "<urlset") || !strings.Contains(string(files["/sitemap-pages.xml"]), "&amp;") || !strings.Contains(string(files["/sitemap-sites-1.xml"]), "<lastmod>") {
		t.Fatal("single mode lost child maps, escaping or dates")
	}
}

func TestSiteIndexingPreferences(t *testing.T) {
	for _, tc := range []struct {
		name, robots, html string
		want               bool
	}{
		{"missing index", "", "", false},
		{"open", "", "<h1>hello</h1>", true},
		{"robots root", "User-agent: *\nDisallow: /\n", "hello", false},
		{"other group", "User-agent: other\nDisallow: /\nUser-agent: *\nAllow: /\n", "hello", true},
		{"other meta", "", `<meta name='description' content='noindex'>`, true},
		{"meta none", "", `<meta name="robots" content="none">`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			disk, err := storage.NewDiskStorage(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			dir := disk.VersionDir("owner", "site", 1)
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			if tc.html != "" {
				if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(tc.html), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.robots != "" {
				if err := os.WriteFile(filepath.Join(dir, "robots.txt"), []byte(tc.robots), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if got := siteAllowsIndexing(context.Background(), disk, storage.FileVersion{UserID: "owner", SiteName: "site", Version: 1}); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
