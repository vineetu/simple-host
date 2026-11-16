package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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

type productAssets struct {
	replace map[string]string
	// own are the product's own file names (name -> file in files). The page
	// pipeline reads them like any static name; over HTTP only the handler
	// ProductFiles returns answers them.
	own map[string]string
	// fileServer answers the own names; RegisterUIRoutes builds it.
	fileServer http.Handler
	files      fs.FS
	skills     fs.FS
	hooks      ProductAssetHooks
	rewrite    func(string, []byte) []byte
	// styleVersion is the cache-busting version of the product's stylesheet,
	// worked out once when the contribution is installed.
	styleVersion string
}

// productStylesheet is the name chrome pages link the product's stylesheet by.

// SetProductAssets installs the contribution before routes or shared pages
// are built. replace maps platform names to product files; own lists the
// product's own names, which only ProductFiles answers over HTTP.
func (h *SiteHandler) SetProductAssets(replace, own map[string]string, files, skills fs.FS, hooks ProductAssetHooks, rewrite func(string, []byte) []byte) {
	p := h.pipeline()
	p.product = productAssets{replace: replace, own: own, files: files, skills: skills, hooks: hooks, rewrite: rewrite}
	if file, ok := p.product.file(p.pres.StylePath); ok && files != nil {
		if b, err := fs.ReadFile(files, file); err == nil {
			sum := sha256.Sum256(b)
			p.product.styleVersion = hex.EncodeToString(sum[:])[:10]
		}
	}
	p.text.Clear()
	// Chrome pages read through the product's files and Rewrite from here on.
	chromeCache.Clear()
}
func (h *SiteHandler) AssetServices() AssetServices { return h.pipeline() }

// file is the product file behind a static name: a replaced platform name
// first, then one of the product's own names. There is no fallback from a
// listed name to the platform's file.
func (a productAssets) file(name string) (string, bool) {
	if file, ok := a.replace[name]; ok {
		return file, true
	}
	file, ok := a.own[name]
	return file, ok
}

// ProductFiles answers the product's own names through the file server
// RegisterUIRoutes built for them; before that, and with no own names, 404.
func (p *assetPipeline) ProductFiles() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p.product.fileServer == nil {
			http.NotFound(w, r)
			return
		}
		p.product.fileServer.ServeHTTP(w, r)
	})
}

// Static returns a platform file as the product-neutral view serves it: the
// old base-domain swap (baseText), and for signin.js and auth.js the sign-in
// splices (the message table, the sign-in card and auth.js's base patch),
// then the instance host pass for the names in rewrittenAssets. It does not
// run a product's Rewrite or the limits pass.
func (p *assetPipeline) Static(name string) ([]byte, bool) {
	b, err := staticFiles.ReadFile("static/" + name)
	if err == nil && slices.Contains(rewrittenAssets, name) {
		b = instanceHosts.apply(b)
	}
	return b, err == nil
}

// InstanceText is, exactly: the old base-domain swap (baseText, a no-op for
// text that does not name simple-host.site), then the instance host pass
// only when no product InstanceText hook is installed, then the limits pass.
func (p *assetPipeline) InstanceText(b []byte) []byte {
	b = baseText(b)
	// Products apply their own origin through the InstanceText hook, as the
	// existing product skill pipeline does. Platform text uses the host pass.
	if p.product.hooks.InstanceText == nil {
		b = instanceHosts.apply(b)
	}
	return p.limitsText.apply(b)
}
func (p *assetPipeline) PluginVersion() string {
	if p.pres.PluginVersion != "" {
		return p.pres.PluginVersion
	}
	v, _ := PluginVersion()
	return v
}
func (p *assetPipeline) ServeCachedZip(build func() ([]byte, error), filename string) http.HandlerFunc {
	var cache cachedZip
	return func(w http.ResponseWriter, r *http.Request) {
		cache.once.Do(func() { cache.bytes, cache.err = build() })
		if cache.err != nil {
			http.Error(w, http.StatusText(500), 500)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		http.ServeContent(w, r, filename, skillsModTime, bytes.NewReader(cache.bytes))
	}
}

func (p *assetPipeline) productInstanceText(b []byte) []byte {
	if hook := p.product.hooks.InstanceText; hook != nil {
		return hook(b)
	}
	return b
}

type assetFiles struct{ fs.FS }

func (f assetFiles) ReadFile(name string) ([]byte, error) { return fs.ReadFile(f.FS, name) }

func (p *assetPipeline) productStyleVersion() string { return p.product.styleVersion }
