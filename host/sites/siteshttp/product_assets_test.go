package siteshttp

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/web"

	"github.com/vsriram/simple-host/host/edition"
)

func TestProductAssetPrecedence(t *testing.T) {
	platform, err := fs.ReadFile(web.EmbeddedTree, "static/llms.txt")
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
			sh := withHosts(&SiteHandler{Ed: edition.Default(config.DefaultLimits())})
			sh.SetProductAssets(tc.replace, nil, tc.files, nil, web.ProductAssetHooks{}, nil)
			got, err := fs.ReadFile(sh.Pages().TextFS(), "static/llms.txt")
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

func TestProductOnlyAssetRequiresRoute(t *testing.T) {
	sh := chromeTestHandler(platformTestEdition())
	sh.SetProductAssets(nil, nil, fstest.MapFS{"product-only.js": {Data: []byte("private source")}}, nil, web.ProductAssetHooks{}, nil)
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
	p := web.NewAssetPipeline(edition.Default(config.DefaultLimits()), "")
	if got := p.ProductInstanceText([]byte("original")); string(got) != "original" {
		t.Fatal("nil text hook changed bytes")
	}
	if !p.SkillAvailable("website-deploy") || p.SkillAvailable("run-hackathon") {
		t.Fatal("nil skill hook changed platform availability")
	}
	// With no replacement, the platform's public spec is served.
	r0 := httptest.NewRecorder()
	p.ServeRewrittenAsset("openapi.json", nil, web.SkillsModTime).ServeHTTP(r0, httptest.NewRequest("GET", "/openapi.json", nil))
	if r0.Code != http.StatusOK || !strings.Contains(r0.Body.String(), `"openapi"`) {
		t.Fatalf("platform spec: %d", r0.Code)
	}
	// No PersonalPresentationHTML hook on a product install: the page text
	// passes through the shared presentation unchanged.
	pp := web.NewAssetPipeline(groupTestEdition(), "")
	if got := pp.PresentChromePage("index.html", []byte("<p>plain</p>")); string(got) != "<p>plain</p>" {
		t.Fatalf("nil presentation hook changed the page: %s", got)
	}
	sh := withHosts(&SiteHandler{Ed: edition.Default(config.DefaultLimits()), Assets: p})
	sh.SetProductAssets(map[string]string{"openapi.json": "static/missing.json"}, nil, fstest.MapFS{}, nil, web.ProductAssetHooks{}, nil)
	r := httptest.NewRecorder()
	p.ServeRewrittenAsset("openapi.json", nil, web.SkillsModTime).ServeHTTP(r, httptest.NewRequest("GET", "/openapi.json", nil))
	if r.Code != http.StatusNotFound {
		t.Fatalf("missing replacement: %d", r.Code)
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
	sh.SetProductAssets(nil, own, files, nil, web.ProductAssetHooks{}, nil)
	if b, err := fs.ReadFile(sh.Pages().TextFS(), "static/own.js"); err != nil || string(b) != "own source" {
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
		sh.SetProductAssets(nil, nil, nil, nil, web.ProductAssetHooks{CatalogPlugin: tc.hook}, nil)
		r := httptest.NewRecorder()
		sh.Pipeline().ServeSkillsCatalog("https://simple-host.app").ServeHTTP(r, httptest.NewRequest("GET", "/v1/skills", nil))
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
