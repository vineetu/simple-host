package siteshttp

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/vsriram/simple-host/host/web"
	"io/fs"
)

// productStylesheet is the name chrome pages link the product's stylesheet by.

// SetProductAssets installs the contribution before routes or shared pages
// are built. replace maps platform names to product files; own lists the
// product's own names, which only ProductFiles answers over HTTP.
func (h *SiteHandler) SetProductAssets(replace, own map[string]string, files, skills fs.FS, hooks web.ProductAssetHooks, rewrite func(string, []byte) []byte) {
	p := h.Pipeline()
	p.Product = web.ProductAssets{Replace: replace, Own: own, Files: files, Skills: skills, Hooks: hooks, Rewrite: rewrite}
	if file, ok := p.Product.File(p.Pres.StylePath); ok && files != nil {
		if b, err := fs.ReadFile(files, file); err == nil {
			sum := sha256.Sum256(b)
			p.Product.StyleVersion = hex.EncodeToString(sum[:])[:10]
		}
	}
	p.Text.Clear()
	// Chrome pages read through the product's files and Rewrite from here on.
	web.ChromeCache.Clear()
}
func (h *SiteHandler) AssetServices() web.AssetServices { return h.Pipeline() }
