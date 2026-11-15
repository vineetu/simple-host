package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/vsriram/simple-host/internal/edition"
)

func TestProductAssetPrecedence(t *testing.T) {
	platform, err := fs.ReadFile(embeddedStatic, "static/llms.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		replace map[string]string
		files   fstest.MapFS
		want    string
		missing bool
	}{
		{name: "nil uses platform", want: string(platform)},
		{name: "unlisted collision uses platform", files: fstest.MapFS{"static/llms.txt": {Data: []byte("unlisted")}}, want: string(platform)},
		{name: "explicit replacement", replace: map[string]string{"llms.txt": "own.txt"}, files: fstest.MapFS{"own.txt": {Data: []byte("product")}}, want: "product"},
		{name: "missing replacement never falls back", replace: map[string]string{"llms.txt": "missing.txt"}, files: fstest.MapFS{}, missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sh := &SiteHandler{ed: edition.ForKind(edition.Host)}
			sh.SetProductAssets(tc.replace, tc.files, nil, ProductAssetHooks{}, nil)
			got, err := fs.ReadFile(sh.Pages().StaticFS(), "static/llms.txt")
			if tc.missing {
				if !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("missing replacement: %v", err)
				}
				return
			}
			if err != nil || string(got) != tc.want {
				t.Fatalf("selection error=%v, got %q", err, got)
			}
		})
	}
}

// The docs-site entry of Replace names the product's docs tree. It is read
// only by docsTree: the static file lookup never sees it, so the file server
// misses /docs-site exactly as it does on an install with no product (the
// request then falls through to the handle lookup and its 404, not to a
// redirect into the product's folder).
func TestProductDocsRootIsNotAStaticName(t *testing.T) {
	files := fstest.MapFS{"docs/own/index.html": {Data: []byte("product docs")}}
	sh := chromeTestHandler(edition.Host)
	sh.SetProductAssets(map[string]string{docsSiteRoot: "docs/own"}, files, nil, ProductAssetHooks{}, nil)
	for _, name := range []string{"static/docs-site", "static/docs-site/index.html"} {
		if _, err := fs.Stat(sh.Pages().StaticFS(), name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("static lookup of %s reached the docs root: %v", name, err)
		}
	}
	if _, err := fs.ReadDir(sh.Pages().StaticFS(), "static/docs-site"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("static directory read reached the docs root: %v", err)
	}
	if body, err := fs.ReadFile(sh.Pages().docsTree(), "index.html"); err != nil || string(body) != "product docs" {
		t.Errorf("docsTree: %q, %v", body, err)
	}
}

func TestProductOnlyAssetRequiresRoute(t *testing.T) {
	sh := chromeTestHandler(edition.Host)
	sh.SetProductAssets(nil, fstest.MapFS{"product-only.js": {Data: []byte("private source")}}, nil, ProductAssetHooks{}, nil)
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "https://simple-host.app", sh)
	if r := get(t, mux, "", "/product-only.js"); r.Code != 404 || strings.Contains(r.Body.String(), "private source") {
		t.Fatalf("unrouted product file exposed: %d", r.Code)
	}
	mux.HandleFunc("GET /product-only.js", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("routed")) })
	if r := get(t, mux, "", "/product-only.js"); r.Code != 200 || r.Body.String() != "routed" {
		t.Fatalf("product route: %d %q", r.Code, r.Body.String())
	}
}

func TestProductAssetHookDefaultsAndErrors(t *testing.T) {
	p := newAssetPipeline(edition.ForKind(edition.Host), "")
	if got := p.productInstanceText([]byte("original")); string(got) != "original" {
		t.Fatal("nil text hook changed bytes")
	}
	if !p.skillAvailable("website-deploy") || p.skillAvailable("run-hackathon") {
		t.Fatal("nil skill hook changed platform availability")
	}
	// No OpenAPISpec hook: the platform's own public spec is served.
	r0 := httptest.NewRecorder()
	p.serveRewrittenAsset("openapi.json", nil, skillsModTime).ServeHTTP(r0, httptest.NewRequest("GET", "/openapi.json", nil))
	if r0.Code != http.StatusOK || !strings.Contains(r0.Body.String(), `"openapi"`) {
		t.Fatalf("nil spec hook: %d", r0.Code)
	}
	// No PersonalPresentationHTML hook on a product install: the page text
	// passes through the shared presentation unchanged.
	pp := newAssetPipeline(edition.ForKind(edition.Hack), "")
	if got := pp.presentChromePage("index.html", []byte("<p>plain</p>")); string(got) != "<p>plain</p>" {
		t.Fatalf("nil presentation hook changed the page: %s", got)
	}
	sh := &SiteHandler{ed: edition.ForKind(edition.Host), assets: p}
	sh.SetProductAssets(nil, nil, nil, ProductAssetHooks{OpenAPISpec: func([]byte) ([]byte, error) { return nil, errors.New("product spec unavailable") }}, nil)
	r := httptest.NewRecorder()
	p.serveRewrittenAsset("openapi.json", nil, skillsModTime).ServeHTTP(r, httptest.NewRequest("GET", "/openapi.json", nil))
	if r.Code != http.StatusNotFound {
		t.Fatalf("hook failure: %d", r.Code)
	}
}

func TestAssetServicesInstanceRewriting(t *testing.T) {
	old := instanceHosts
	instanceHosts = newHostRewriter("assets.example", "sites.assets.example", "")
	t.Cleanup(func() { instanceHosts = old })
	p := newAssetPipeline(edition.ForKind(edition.Host), "")
	body, ok := p.Static("llms.txt")
	if !ok || !strings.Contains(string(body), "assets.example") {
		t.Fatal("Static omitted the instance host rewrite")
	}
	if got := p.InstanceText([]byte("https://simple-host.app/mcp")); string(got) != "https://assets.example/mcp" {
		t.Fatalf("InstanceText: %s", got)
	}
}

// The product stylesheet's version is worked out once, when the contribution
// is installed, from the file its Replace entry names.
func TestProductStyleVersionComputedAtInstall(t *testing.T) {
	sh := chromeTestHandler(edition.Host)
	if got := sh.pipeline().productStyleVersion(); got != "" {
		t.Fatalf("no product: version %q", got)
	}
	files := fstest.MapFS{"own.css": {Data: []byte("body{}")}}
	sh.SetProductAssets(map[string]string{productStylesheet: "own.css"}, files, nil, ProductAssetHooks{}, nil)
	sum := sha256.Sum256([]byte("body{}"))
	want := hex.EncodeToString(sum[:])[:10]
	if got := sh.pipeline().productStyleVersion(); got != want {
		t.Fatalf("version %q, want %q", got, want)
	}
	files["own.css"] = &fstest.MapFile{Data: []byte("changed")}
	if got := sh.pipeline().productStyleVersion(); got != want {
		t.Fatalf("version recomputed after install: %q", got)
	}
}
