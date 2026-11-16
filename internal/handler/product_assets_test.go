package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/vsriram/simple-host/internal/config"
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
			sh := &SiteHandler{ed: edition.Default(config.DefaultLimits())}
			sh.SetProductAssets(tc.replace, nil, tc.files, nil, ProductAssetHooks{}, nil)
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
	sh := chromeTestHandler(platformTestEdition())
	sh.SetProductAssets(map[string]string{docsSiteRoot: "docs/own"}, nil, files, nil, ProductAssetHooks{}, nil)
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
	sh := chromeTestHandler(platformTestEdition())
	sh.SetProductAssets(nil, nil, fstest.MapFS{"product-only.js": {Data: []byte("private source")}}, nil, ProductAssetHooks{}, nil)
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
	p := newAssetPipeline(edition.Default(config.DefaultLimits()), "")
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
	pp := newAssetPipeline(groupTestEdition(), "")
	if got := pp.presentChromePage("index.html", []byte("<p>plain</p>")); string(got) != "<p>plain</p>" {
		t.Fatalf("nil presentation hook changed the page: %s", got)
	}
	sh := &SiteHandler{ed: edition.Default(config.DefaultLimits()), assets: p}
	sh.SetProductAssets(nil, nil, nil, nil, ProductAssetHooks{OpenAPISpec: func([]byte) ([]byte, error) { return nil, errors.New("product spec unavailable") }}, nil)
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
	p := newAssetPipeline(edition.Default(config.DefaultLimits()), "")
	body, ok := p.Static("llms.txt")
	if !ok || !strings.Contains(string(body), "assets.example") {
		t.Fatal("Static omitted the instance host rewrite")
	}
	if got := p.InstanceText([]byte("https://simple-host.app/mcp")); string(got) != "https://assets.example/mcp" {
		t.Fatalf("InstanceText: %s", got)
	}
}

// The product stylesheet's version is worked out once, when the contribution
// is installed, from the file the product's own entry names.
func TestProductStyleVersionComputedAtInstall(t *testing.T) {
	sh := chromeTestHandler(groupTestEdition())
	if got := sh.pipeline().productStyleVersion(); got != "" {
		t.Fatalf("no product: version %q", got)
	}
	files := fstest.MapFS{"own.css": {Data: []byte("body{}")}}
	sh.SetProductAssets(nil, map[string]string{"product.css": "own.css"}, files, nil, ProductAssetHooks{}, nil)
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

// A product's own name is read by the page pipeline, is never answered by
// the platform's file server, and is answered by ProductFiles only at an
// address where the product mounted it.
func TestProductOwnFilesAreServedOnlyByTheProductRoute(t *testing.T) {
	files := fstest.MapFS{
		"files/own.js":       {Data: []byte("own source")},
		"files/own.kit/a.js": {Data: []byte("kit a")},
		"files/own.kit":      {Mode: fs.ModeDir},
	}
	own := map[string]string{"own.js": "files/own.js", "own.kit": "files/own.kit", "own.kit/a.js": "files/own.kit/a.js"}
	sh := chromeTestHandler(platformTestEdition())
	sh.SetProductAssets(nil, own, files, nil, ProductAssetHooks{}, nil)
	if b, err := fs.ReadFile(sh.Pages().StaticFS(), "static/own.js"); err != nil || string(b) != "own source" {
		t.Fatalf("pipeline read of an own name: %q, %v", b, err)
	}
	// Mounted before the platform's routes exist, as a product's Routes is.
	productFiles := sh.AssetServices().ProductFiles()
	early := httptest.NewRecorder()
	productFiles.ServeHTTP(early, httptest.NewRequest("GET", "/own.js", nil))
	if early.Code != http.StatusNotFound {
		t.Fatalf("ProductFiles before the routes are built: %d", early.Code)
	}
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "https://simple-host.app", sh)
	for _, path := range []string{"/own.js", "/own.kit/a.js", "/own.kit/"} {
		if r := get(t, mux, "", path); r.Code != http.StatusNotFound || strings.Contains(r.Body.String(), "own source") || strings.Contains(r.Body.String(), "a.js") {
			t.Errorf("%s answered by the platform's file server: %d", path, r.Code)
		}
	}
	mux.Handle("GET /own.js", productFiles)
	mux.Handle("GET /own.kit/a.js", productFiles)
	if r := get(t, mux, "", "/own.js"); r.Code != http.StatusOK || r.Body.String() != "own source" {
		t.Errorf("mounted own file: %d %q", r.Code, r.Body.String())
	}
	if r := get(t, mux, "", "/own.kit/a.js"); r.Code != http.StatusOK || r.Body.String() != "kit a" {
		t.Errorf("mounted own file in a folder: %d %q", r.Code, r.Body.String())
	}
	// The same handler mounted at a name that is not in the table finds nothing.
	mux.Handle("GET /unlisted.js", productFiles)
	if r := get(t, mux, "", "/unlisted.js"); r.Code != http.StatusNotFound {
		t.Errorf("unlisted name through the product route: %d", r.Code)
	}
}

// With no product, or a product with no own names, ProductFiles answers 404.
func TestProductFilesWithoutOwnNames(t *testing.T) {
	sh := chromeTestHandler(platformTestEdition())
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "https://simple-host.app", sh)
	r := httptest.NewRecorder()
	sh.AssetServices().ProductFiles().ServeHTTP(r, httptest.NewRequest("GET", "/site.css", nil))
	if r.Code != http.StatusNotFound {
		t.Fatalf("ProductFiles with no own names: %d", r.Code)
	}
}

// The skills catalogue reports the platform's plugin name unless a product
// names its own.
func TestSkillsCatalogPluginName(t *testing.T) {
	for _, tc := range []struct{ hook, want string }{{"", `"plugin":"website-deploy"`}, {"own-plugin", `"plugin":"own-plugin"`}} {
		sh := chromeTestHandler(platformTestEdition())
		sh.SetProductAssets(nil, nil, nil, nil, ProductAssetHooks{CatalogPlugin: tc.hook}, nil)
		r := httptest.NewRecorder()
		sh.pipeline().serveSkillsCatalog("https://simple-host.app").ServeHTTP(r, httptest.NewRequest("GET", "/v1/skills", nil))
		if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), tc.want) {
			t.Errorf("hook %q: %d %s", tc.hook, r.Code, r.Body.String())
		}
	}
}

// The platform's plugin archive and installers are the platform's routes: a
// product install does not get them from here (the product answers those
// addresses itself).
func TestPlatformPluginDownloadsAreNotRegisteredForAProduct(t *testing.T) {
	host := http.NewServeMux()
	RegisterUIRoutes(host, "https://simple-host.app", chromeTestHandler(platformTestEdition()))
	product := http.NewServeMux()
	RegisterUIRoutes(product, "https://simple-host.app", chromeTestHandler(groupTestEdition()))
	for _, path := range []string{"/plugin.zip", "/install.sh", "/install.ps1"} {
		req := httptest.NewRequest("GET", path, nil)
		if _, pattern := host.Handler(req); pattern != "GET "+path {
			t.Errorf("platform: %s is answered by %q", path, pattern)
		}
		if r := get(t, host, "", path); r.Code != http.StatusOK {
			t.Errorf("platform: %s: %d", path, r.Code)
		}
		if _, pattern := product.Handler(req); pattern == "GET "+path {
			t.Errorf("product install: the platform registered %s", path)
		}
	}
}
