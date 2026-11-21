package siteshttp

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/vsriram/simple-host/host/web"
)

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
