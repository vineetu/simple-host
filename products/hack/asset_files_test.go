package hack

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"

	productedition "github.com/vsriram/simple-host/products/hack/edition"

	"github.com/vsriram/simple-host/host/handler"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
	"github.com/vsriram/simple-host/host/web"
	"github.com/vsriram/simple-host/host/webassets"
	hackassets "github.com/vsriram/simple-host/products/hack/webassets"
)

func TestStoragePanelSharesNoticeGate(t *testing.T) {
	sh, _, _ := handler.NewSiteHandlers(productedition.ForKind(productedition.Hack), "", nil, nil, nil, "simple-hack.app", "sites.simple-hack.app", "", "", "", "", nil, 0, "", nil, nil)
	assets := Extension().Assets(sh)
	sh.SetProductAssets(assets.Replace, assets.Own, assets.Files, assets.Skills, assets.Hooks, assets.Rewrite)
	page, err := sh.Pages().ChromePage(httptest.NewRequest(http.MethodGet, "/owner", nil), "showcase.html")
	if err != nil {
		t.Fatal(err)
	}
	shared, err := webassets.Static.ReadFile("static/showcase.html")
	if err != nil {
		t.Fatal(err)
	}
	panel := func(page []byte) []byte {
		start := bytes.Index(page, []byte("async function openStorage("))
		if start < 0 {
			t.Fatal("Storage panel missing")
		}
		end := bytes.Index(page[start:], []byte("var ruleEditors"))
		if end < 0 {
			t.Fatal("Storage cards missing")
		}
		return page[start : start+end]
	}
	if !bytes.Equal(panel(page), panel(shared)) {
		t.Fatal("Hack must use the same Storage notice gate as Host")
	}
}

// The two tables keep the contract's meaning: Replace holds platform names
// only, Own holds names the platform does not have, no name is in both, and
// every entry points at a file Hack embeds.
func TestAssetTablesNameTheRightFiles(t *testing.T) {
	replace, own := assetReplacements(), ownAssetFiles()
	for name, file := range replace {
		if _, both := own[name]; both {
			t.Errorf("%s is in both tables", name)
		}
		if _, err := fs.Stat(hackassets.Static, file); err != nil {
			t.Errorf("Replace %s: %v", name, err)
		}
		if name == "openapi.json" {
			continue // the platform serves its openapi.yaml at this address
		}
		if _, err := fs.Stat(webassets.Static, "static/"+name); err != nil {
			t.Errorf("Replace lists %s, which is not a platform name", name)
		}
	}
	for name, file := range own {
		if file != "static/"+name {
			t.Errorf("Own %s points at %s", name, file)
		}
		if _, err := fs.Stat(productFiles{}, file); err != nil {
			t.Errorf("Own %s: %v", name, err)
		}
		if _, err := fs.Stat(webassets.Static, "static/"+name); err == nil {
			t.Errorf("Own lists %s, which the platform also has", name)
		}
	}
	served, pages := servedOwnFiles(), pageOnlyFiles()
	if len(served)+len(pages) != len(own) {
		t.Errorf("a name is both served by file name and page-only")
	}
	var folders []string
	for name, file := range served {
		if info, err := fs.Stat(hackassets.Static, file); err == nil && info.IsDir() {
			folders = append(folders, name)
		}
	}
	sort.Strings(folders)
	if !reflect.DeepEqual(folders, ownFolders) {
		t.Errorf("ownFolders is %v, the served folders are %v", ownFolders, folders)
	}
}

func TestSkillsVersionRoutes(t *testing.T) {
	hostVersion, err := web.PluginVersion()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind    productedition.Kind
		version string
	}{
		{productedition.Host, hostVersion},
		{productedition.Hack, "0.27.20"},
	} {
		t.Run(tc.kind.String(), func(t *testing.T) {
			sh, _, _ := handler.NewSiteHandlers(productedition.ForKind(tc.kind), "", nil, nil, nil, "simple-host.app", "sites.simple-host.app", "", "", "", "", nil, 0, "", nil, nil)
			mux := http.NewServeMux()
			if tc.kind == productedition.Hack {
				assets := Extension().Assets(sh)
				sh.SetProductAssets(assets.Replace, assets.Own, assets.Files, assets.Skills, assets.Hooks, assets.Rewrite)
				assets.Routes(mux, sh.AssetServices())
				if _, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, "/skills/version", nil)); pattern != "GET /skills/version" {
					t.Fatalf("product did not register the version route: %q", pattern)
				}
			}
			siteshttp.RegisterUIRoutes(mux, "https://simple-host.app", sh)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/skills/version", nil))
			if rec.Code != http.StatusOK || rec.Body.String() != `{"version":"`+tc.version+`"}` {
				t.Fatalf("version response: %d %q", rec.Code, rec.Body.String())
			}
			if want := (http.Header{"Content-Type": {"application/json"}}); !reflect.DeepEqual(rec.Header(), want) {
				t.Fatalf("version headers: %v, want %v", rec.Header(), want)
			}
		})
	}
}

// Hack's one file route answers every own name that is served by its file
// name, at that name; a page-only name gets no address of its own.
func TestOwnFilesAreServedByTheFileRoute(t *testing.T) {
	sh, _, _ := handler.NewSiteHandlers(productedition.ForKind(productedition.Hack), "", nil, nil, nil, "simple-host.app", "sites.simple-host.app", "", "", "", "", nil, 0, "", nil, nil)
	assets := Extension().Assets(sh)
	sh.SetProductAssets(assets.Replace, assets.Own, assets.Files, assets.Skills, assets.Hooks, assets.Rewrite)
	mux := http.NewServeMux()
	assets.Routes(mux, sh.AssetServices())
	siteshttp.RegisterUIRoutes(mux, "https://simple-hack.app", sh)
	for name, file := range assets.Own {
		req := httptest.NewRequest(http.MethodGet, "/"+name, nil)
		_, pattern := mux.Handler(req)
		if _, pageOnly := pageOnlyFiles()[name]; pageOnly {
			if pattern == "GET /"+name {
				t.Errorf("%s is page-only and has its own file route", name)
			}
			continue
		}
		if pattern != "GET /"+name {
			t.Errorf("%s is answered by %q, not by the file route", name, pattern)
			continue
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		info, err := fs.Stat(productFiles{}, file)
		if err != nil {
			t.Fatal(err)
		}
		if info.IsDir() {
			if rec.Code != http.StatusMovedPermanently {
				t.Errorf("folder %s: %d", name, rec.Code)
			}
			if _, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, "/"+name+"/", nil)); pattern != "GET /"+name+"/{$}" {
				t.Errorf("folder %s/ is answered by %q", name, pattern)
			}
			continue
		}
		if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Errorf("%s: %d, %d bytes", name, rec.Code, rec.Body.Len())
		}
	}
	// The film files are in neither table: each has its own exact route.
	if _, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, "/film/hack-film-390.mp4", nil)); pattern != "GET /film/hack-film-390.mp4" {
		t.Errorf("the film is answered by %q", pattern)
	}
}

// A wrong address on a hackathon platform shows Simple Hack's own not-found
// page: no Simple Host header or theme menu, and no link to another service.
func TestNotFoundIsSimpleHacksOwn(t *testing.T) {
	sh, _, _ := handler.NewSiteHandlers(productedition.ForKind(productedition.Hack), "", nil, nil, nil, "simple-hack.app", "sites.simple-hack.app", "", "", "", "", nil, 0, "", nil, nil)
	assets := Extension().Assets(sh)
	sh.SetProductAssets(assets.Replace, assets.Own, assets.Files, assets.Skills, assets.Hooks, assets.Rewrite)
	rec := httptest.NewRecorder()
	sh.RenderNotFound(rec, httptest.NewRequest(http.MethodGet, "/no-such.page", nil), "/no-such.page")
	body := rec.Body.String()
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{"simple<b>·</b>hack", `href="/">Go to Simple Hack</a>`, "Page not found"} {
		if !bytes.Contains([]byte(body), []byte(want)) {
			t.Errorf("not-found page lacks %q", want)
		}
	}
	for _, bad := range []string{"simple-host.app", "data-theme", "Dark"} {
		if bytes.Contains([]byte(body), []byte(bad)) {
			t.Errorf("not-found page has %q", bad)
		}
	}
}
