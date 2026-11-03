package handler

import (
	"strings"
	"sync"
	"weak"

	"github.com/vsriram/simple-host/internal/edition"
)

// canonicalHackBaseURL is simple-hack.app's own address: the hack ink base
// every Hack instance starts from (SetHackInstanceURL moves it).
const canonicalHackBaseURL = "https://simple-hack.app"

// assetPipeline is everything the static asset and chrome pipeline reads that
// depends on the product or the instance: which product's assets and copy it
// serves, the hack chrome, the hack instance's own address, and the cache of
// swapped embedded files. A SiteHandler owns one (h.assets), built from its
// Edition in NewSiteHandler; the pipeline's methods read only these fields.
//
// The base-swap settings (baseTextTo, authJSBases, instanceIsHostedService,
// the host rewriter) stay package settings: they describe the instance's
// addresses, not the product.
type assetPipeline struct {
	// pres is the edition's presentation policy: HackAssets selects Simple
	// Hack's skills, auth.js, docs and copy.
	pres edition.PresentationPolicy
	// hostedEvents is the edition's Routes().HostedEvents (pricing numbers).
	hostedEvents bool
	// chromeHack renders the simple-hack.app header, footer and ink (the
	// SetHackChrome setting).
	chromeHack bool
	// inkBase is this Hack instance's public address (SetHackInstanceURL),
	// canonicalHackBaseURL on simple-hack.app itself.
	inkBase string
	// text holds swapped copies of embedded files, by base and name
	// (baseTextFS.Open).
	text sync.Map
}

// newAssetPipeline builds the pipeline for ed (nil = Host), taking the hack
// chrome and instance address from the startup setters, and registers it so
// later setter calls and base-text changes reach it.
func newAssetPipeline(ed *edition.Edition) *assetPipeline {
	p := &assetPipeline{
		pres:         ed.Presentation(),
		hostedEvents: ed.Routes().HostedEvents,
	}
	assetRegistry.mu.Lock()
	// The transitional startup settings (hack_mode.go) are the defaults.
	p.chromeHack, p.inkBase = hackChrome, hackInkBaseURL
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

// pipeline is h's asset pipeline. NewSiteHandler builds it; a bare
// &SiteHandler{} (tests) gets one for its edition on first use.
func (h *SiteHandler) pipeline() *assetPipeline {
	h.assetsOnce.Do(func() {
		if h.assets == nil {
			h.assets = newAssetPipeline(h.ed)
		}
	})
	return h.assets
}

// staticFS is the embedded tree as this pipeline serves it.
func (p *assetPipeline) staticFS() baseTextFS {
	return baseTextFS{fsys: embeddedStatic, p: p}
}

// assetRegistry tracks every live pipeline, weakly, so the package-level
// startup setters (SetHackChrome, SetHackInstanceURL) and the base-text
// setters (setBaseText, SetSiteBaseText), which have no handler in reach,
// still reach each instance's pipeline: the setters update its fields and
// clear its swapped-file cache, as they did the package globals before.
var assetRegistry struct {
	mu   sync.Mutex
	live []weak.Pointer[assetPipeline]
}

// eachAssetPipeline calls fn for every live pipeline, holding the registry lock.
func eachAssetPipeline(fn func(*assetPipeline)) {
	assetRegistry.mu.Lock()
	defer assetRegistry.mu.Unlock()
	for _, wp := range assetRegistry.live {
		if p := wp.Value(); p != nil {
			fn(p)
		}
	}
}

// clearAssetText drops every pipeline's swapped-file cache.
func clearAssetText() {
	eachAssetPipeline(func(p *assetPipeline) { p.text.Clear() })
}

// SetHackChrome turns on simple-hack.app chrome. The integrator calls it
// once at startup when EVENTS=hosted.
func SetHackChrome(on bool) {
	assetRegistry.mu.Lock()
	hackChrome = on
	assetRegistry.mu.Unlock()
	eachAssetPipeline(func(p *assetPipeline) { p.chromeHack = on })
}

// SetHackInstanceURL is called once at startup, before routes or ZIP caches.
func SetHackInstanceURL(base string) {
	base = strings.TrimRight(base, "/")
	assetRegistry.mu.Lock()
	hackInkBaseURL = base
	assetRegistry.mu.Unlock()
	eachAssetPipeline(func(p *assetPipeline) { p.inkBase = base })
	chromeCache.Clear()
}
