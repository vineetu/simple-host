package web

import (
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"
	"weak"

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
)

// AssetPipeline is everything the static asset and chrome pipeline reads that
// depends on the product or the instance: which product's assets and copy it
// serves, the product chrome, the product instance's own address, and the
// cache of swapped embedded files. A SiteHandler owns one (h.Assets), built
// from its Edition in NewSiteHandlers. Its fields are set at startup (the
// product's assets once they are installed) and only read while serving.
//
// The base-swap settings (baseTextTo, authJSBases, InstanceIsHostedService,
// the host rewriter) stay package settings: they describe the instance's
// addresses, not the product.
type AssetPipeline struct {
	Product ProductAssets
	// Pres is the edition's presentation policy: ProductAssets selects Simple
	// Hack's skills, auth.js and copy.
	Pres edition.PresentationPolicy
	// hostedEvents is the edition's Routes().HostedEvents (pricing numbers).
	hostedEvents bool
	// limits is the edition's Limits() (pricing.go's pricingNumbers); limitsText
	// is the same snapshot as a served-text rewriter (limitstext.go). Both
	// come from the one ed.Limits() call in NewAssetPipeline, so formatting
	// never drifts from enforcement.
	limits     edition.Limits
	limitsText *limitsRewriter
	// ChromeProduct renders the product's header, footer and ink: always
	// Pres.ProductAssets, kept as its own field because that is the question
	// this pipeline's callers ask.
	ChromeProduct bool
	// inkBase is this product instance's public address (the instanceURL
	// constructor argument). Meaningless when !ChromeProduct.
	inkBase string
	// text holds swapped copies of embedded files, by base and name
	// (baseTextFS.Open).
	Text sync.Map

	// skillsZip and pluginZip are this instance's platform downloads,
	// each built once. Product download caches belong to the product.
	skillsZip cachedZip
	pluginZip cachedZip
}

// cachedZip is a sync.Once-guarded zip build: bytes on success, err on
// failure, cached either way so a later call does not redo the walk.
type cachedZip struct {
	once  sync.Once
	bytes []byte
	err   error
}

// NewAssetPipeline builds the pipeline for ed (nil = Host) and its product
// instance address ("" = the edition's CanonicalBaseURL), and
// registers it so the base-text setters, which have no handler in reach,
// still reach its swapped-file cache.
func NewAssetPipeline(ed *edition.Edition, instanceURL string) *AssetPipeline {
	if instanceURL == "" {
		instanceURL = ed.Presentation().CanonicalBaseURL
	}
	lim := ed.Limits()
	p := &AssetPipeline{
		Pres:          ed.Presentation(),
		hostedEvents:  ed.Routes().HostedEvents,
		limits:        lim,
		limitsText:    newLimitsRewriter(lim),
		ChromeProduct: ed.Presentation().ProductAssets,
		inkBase:       strings.TrimRight(instanceURL, "/"),
	}
	assetRegistry.mu.Lock()
	live := assetRegistry.live[:0]
	for _, wp := range assetRegistry.live {
		if wp.Value() != nil {
			live = append(live, wp)
		}
	}
	assetRegistry.live = append(live, weak.Make(p))
	assetRegistry.mu.Unlock()
	return p
}

// InkBaseURL is the pipeline's product instance address.
func (p *AssetPipeline) InkBaseURL() string { return p.inkBase }

// IsCanonicalInstance reports whether this is simple-hack.app itself, not a
// self-hosted or otherwise addressed instance.
func (p *AssetPipeline) IsCanonicalInstance() bool {
	return p.Pres.CanonicalBaseURL != "" && p.InkBaseURL() == p.Pres.CanonicalBaseURL
}

// StaticFS is TextFS as a plain fs.FS, for the product's hackPages interface.
func (p *AssetPipeline) StaticFS() fs.FS { return p.TextFS() }

// SkillsModTime is the one Last-Modified every skills/plugin download serves.
func (p *AssetPipeline) SkillsModTime() time.Time { return SkillsModTime }

// ChromePage is the static page name as ServeStaticPage builds it, before
// the response's script nonce is stamped: a product route that changes one
// of its own pages for this install starts from it and stamps the result
// (httpx.StampNonce).
func (p *AssetPipeline) ChromePage(r *http.Request, name string) ([]byte, error) {
	return p.BuildChromePage(name, p.ChromeDataFor(r, ""))
}

// RenderPage applies this instance's chrome and text to a product-owned page.
func (p *AssetPipeline) RenderPage(r *http.Request, name string, raw []byte) ([]byte, error) {
	page, err := withChrome(raw, p.ChromeDataFor(r, ""))
	if err != nil {
		return nil, err
	}
	return httpx.StampNonce(r, p.PresentChromePage(name, page)), nil
}

// Rewritten reports whether the text assets (llms.txt, the spec, ...) need a
// rewriting handler rather than the plain file server.
func (p *AssetPipeline) Rewritten() bool { return InstanceHosts != nil || p.limitsText != nil }

// TextFS is the embedded tree as this pipeline serves it.
func (p *AssetPipeline) TextFS() baseTextFS {
	return baseTextFS{fsys: selectedAssets{p}, p: p}
}

// assetRegistry tracks every live pipeline, weakly, so the base-text setters
// (setBaseText, SetSiteBaseText), which have no handler in reach, can still
// clear every instance's swapped-file cache.
var assetRegistry struct {
	mu   sync.Mutex
	live []weak.Pointer[AssetPipeline]
}

// clearAssetText drops every live pipeline's swapped-file cache.
func clearAssetText() {
	assetRegistry.mu.Lock()
	defer assetRegistry.mu.Unlock()
	for _, wp := range assetRegistry.live {
		if p := wp.Value(); p != nil {
			p.Text.Clear()
		}
	}
}

type selectedAssets struct{ p *AssetPipeline }

func (s selectedAssets) source(name string) (fs.FS, string) {
	if file, ok := s.p.Product.File(strings.TrimPrefix(name, "static/")); ok {
		return s.p.Product.Files, file
	}
	return EmbeddedTree, name
}
func (s selectedAssets) Open(name string) (fs.File, error) {
	tree, file := s.source(name)
	return tree.Open(file)
}
func (s selectedAssets) ReadFile(name string) ([]byte, error) {
	tree, file := s.source(name)
	return fs.ReadFile(tree, file)
}
func (s selectedAssets) ReadDir(name string) ([]fs.DirEntry, error) {
	tree, file := s.source(name)
	return fs.ReadDir(tree, file)
}
