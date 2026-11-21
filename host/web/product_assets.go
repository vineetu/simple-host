package web

import (
	"bytes"
	"io/fs"
	"net/http"
	"slices"
)

// AssetServices exposes the shared mechanics used by product assets.
type AssetServices interface {
	Static(name string) ([]byte, bool)
	InstanceText(data []byte) []byte
	PluginVersion() string
	AdminUICSP(http.Handler) http.Handler
	ServeCachedZip(build func() ([]byte, error), filename string) http.HandlerFunc
	// ProductFiles is the one handler that answers the product's own file
	// names (the own table given to SetProductAssets). It serves them as the
	// platform's file server serves a platform file: the same chrome on HTML
	// pages, the same text rewriting, headers and 404 page. The platform's
	// own file server never answers those names, so each is reachable only
	// at an address where the product mounted this handler.
	ProductFiles() http.Handler
}

// ProductAssetHooks are reverse calls from shared asset code. Nil keeps the platform default.
type ProductAssetHooks struct {
	InstanceText func([]byte) []byte
	// GroupTexts supplies page wording without installing group services,
	// including in the read-only Static process.
	GroupTexts func() GroupTexts
	// RewritePluginZip is declared by the contract and not read by the
	// platform: the product's own archive routes call it directly.
	RewritePluginZip         func([]byte) ([]byte, error)
	SkillAvailable           func(string) bool
	ExpandConnectSteps       func([]byte) []byte
	PersonalPresentationHTML func(string, []byte) []byte
	// CatalogPlugin is the plugin name the skills catalogue (/v1/skills)
	// reports. Empty means the platform's own.
	CatalogPlugin string
}

type ProductAssets struct {
	Replace map[string]string
	// Own are the product's own file names (name -> file in files). The page
	// pipeline reads them like any static name; over HTTP only the handler
	// ProductFiles returns answers them.
	Own map[string]string
	// FileServer answers the own names; RegisterUIRoutes builds it.
	FileServer http.Handler
	Files      fs.FS
	Skills     fs.FS
	Hooks      ProductAssetHooks
	Rewrite    func(string, []byte) []byte
	// StyleVersion is the cache-busting version of the product's stylesheet,
	// worked out once when the contribution is installed.
	StyleVersion string
}

// File is the product file behind a static name: a replaced platform name
// first, then one of the product's own names. There is no fallback from a
// listed name to the platform's file.
func (a ProductAssets) File(name string) (string, bool) {
	if file, ok := a.Replace[name]; ok {
		return file, true
	}
	file, ok := a.Own[name]
	return file, ok
}

// ProductFiles answers the product's own names through the file server
// RegisterUIRoutes built for them; before that, and with no own names, 404.
func (p *AssetPipeline) ProductFiles() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p.Product.FileServer == nil {
			http.NotFound(w, r)
			return
		}
		p.Product.FileServer.ServeHTTP(w, r)
	})
}

// Static returns a platform file as the product-neutral view serves it: the
// old base-domain swap (baseText), and for signin.js and auth.js the sign-in
// splices (the message table, the sign-in card and auth.js's base patch),
// then the instance host pass for the names in RewrittenAssets. It does not
// run a product's Rewrite or the limits pass.
func (p *AssetPipeline) Static(name string) ([]byte, bool) {
	b, err := StaticFiles.ReadFile("static/" + name)
	if err == nil && slices.Contains(RewrittenAssets, name) {
		b = InstanceHosts.Apply(b)
	}
	return b, err == nil
}

// InstanceText is, exactly: the old base-domain swap (baseText, a no-op for
// text that does not name simple-host.site), then the instance host pass
// only when no product InstanceText hook is installed, then the limits pass.
func (p *AssetPipeline) InstanceText(b []byte) []byte {
	b = baseText(b)
	// Products apply their own origin through the InstanceText hook, as the
	// existing product skill pipeline does. Platform text uses the host pass.
	if p.Product.Hooks.InstanceText == nil {
		b = InstanceHosts.Apply(b)
	}
	return p.limitsText.apply(b)
}
func (p *AssetPipeline) PluginVersion() string {
	if p.Pres.PluginVersion != "" {
		return p.Pres.PluginVersion
	}
	v, _ := PluginVersion()
	return v
}
func (p *AssetPipeline) ServeCachedZip(build func() ([]byte, error), filename string) http.HandlerFunc {
	var cache cachedZip
	return func(w http.ResponseWriter, r *http.Request) {
		cache.once.Do(func() { cache.bytes, cache.err = build() })
		if cache.err != nil {
			http.Error(w, http.StatusText(500), 500)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		http.ServeContent(w, r, filename, SkillsModTime, bytes.NewReader(cache.bytes))
	}
}

func (p *AssetPipeline) ProductInstanceText(b []byte) []byte {
	if hook := p.Product.Hooks.InstanceText; hook != nil {
		return hook(b)
	}
	return b
}

type assetFiles struct{ fs.FS }

func (f assetFiles) ReadFile(name string) ([]byte, error) { return fs.ReadFile(f.FS, name) }
