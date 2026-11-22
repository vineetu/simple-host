package siteshttp

import (
	"net/http/httptest"
	"testing"
)

func TestServeSitemapFile(t *testing.T) {
	files := map[string][]byte{"/sitemap.xml": []byte("<urlset/>")}
	r := httptest.NewRecorder()
	serveSitemapFile(r, httptest.NewRequest("HEAD", "/sitemap.xml", nil), files)
	if r.Code != 200 || r.Body.Len() != 0 || r.Header().Get("Content-Length") == "" {
		t.Fatalf("HEAD: %+v", r)
	}
	r = httptest.NewRecorder()
	serveSitemapFile(r, httptest.NewRequest("GET", "/sitemap-sites-99.xml", nil), files)
	if r.Code != 404 {
		t.Fatal(r.Code)
	}
}
