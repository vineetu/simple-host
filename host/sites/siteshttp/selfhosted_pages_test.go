package siteshttp

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/web"

	"github.com/vsriram/simple-host/host/edition"
)

func TestSitemapNamesThisServer(t *testing.T) {
	web.SetInstanceHosts("box.example", "sites.box.example", "")
	defer web.SetInstanceHosts("simple-host.app", "", "")
	policy := edition.PresentationPolicy{CanonicalBaseURL: "https://product.example", HostedHostPrefix: "product.", SitemapPaths: "/"}
	for host, want := range map[string]string{"evil.example": "https://box.example/sitemap.xml", "box.example": "https://box.example/sitemap.xml", "product.example": "https://product.example/sitemap.xml"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/robots.txt", nil)
		req.Host = host
		serveRobotsTxt(rec, req, false, policy)
		if !strings.Contains(rec.Body.String(), "Sitemap: "+want) {
			t.Errorf("%s robots.txt: %q", host, rec.Body.String())
		}
		rec = httptest.NewRecorder()
		req = httptest.NewRequest("GET", "/sitemap.xml", nil)
		req.Host = host
		serveSitemapXML(rec, req, false, policy)
		if !strings.Contains(rec.Body.String(), "<loc>"+strings.TrimSuffix(want, "sitemap.xml")+"</loc>") {
			t.Errorf("%s sitemap.xml does not list its own home page", host)
		}
	}
}
