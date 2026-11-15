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
}

// ProductAssetHooks are reverse calls from shared asset code. Nil keeps the platform default.
type ProductAssetHooks struct {
	InstanceText func([]byte) []byte
	// RewritePluginZip is declared by the contract but not read by the
	// platform yet: the plugin archive routes still build their own (D3-S2b).
	RewritePluginZip         func([]byte) ([]byte, error)
	SkillAvailable           func(string) bool
	ExpandConnectSteps       func([]byte) []byte
	PersonalPresentationHTML func(string, []byte) []byte
	OpenAPISpec              func([]byte) ([]byte, error)
}

type productAssets struct {
	replace map[string]string
	files   fs.FS
	skills  fs.FS
	hooks   ProductAssetHooks
	rewrite func(string, []byte) []byte
	// styleVersion is the cache-busting version of the product's stylesheet,
	// worked out once when the contribution is installed.
	styleVersion string
}

// productStylesheet is the name chrome pages link the product's stylesheet by.
const productStylesheet = "hack-ink.css"

// SetProductAssets installs the contribution before routes or shared pages are built.
func (h *SiteHandler) SetProductAssets(replace map[string]string, files, skills fs.FS, hooks ProductAssetHooks, rewrite func(string, []byte) []byte) {
	p := h.pipeline()
	p.product = productAssets{replace: replace, files: files, skills: skills, hooks: hooks, rewrite: rewrite}
	if file, ok := replace[productStylesheet]; ok && files != nil {
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
func (p *assetPipeline) PluginVersion() string { v, _ := PluginVersion(p.pres.ProductAssets); return v }
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
