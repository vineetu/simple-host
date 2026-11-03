package handler

import (
	"strings"
	"sync"
	"sync/atomic"
	"weak"

	"github.com/vsriram/simple-host/internal/config"
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
	// limits is the edition's Limits() (limits.go's pricingNumbers); limitsText
	// is the same snapshot as a served-text rewriter (limitstext.go). Both
	// come from the one ed.Limits() call in newAssetPipeline, so formatting
	// never drifts from enforcement.
	limits     config.Limits
	limitsText *limitsRewriter
	// chromeHack renders the simple-hack.app header, footer and ink (the
	// SetHackChrome setting). Atomic: a setter may run while requests read.
	chromeHack atomic.Bool
	// inkBase is this Hack instance's public address (SetHackInstanceURL),
	// canonicalHackBaseURL on simple-hack.app itself. Read via inkBaseURL.
	inkBase atomic.Pointer[string]
	// text holds swapped copies of embedded files, by base and name
	// (baseTextFS.Open).
	text sync.Map
}

// newAssetPipeline builds the pipeline for ed (nil = Host), taking the hack
// chrome and instance address from the startup setters, and registers it so
// later setter calls and base-text changes reach it.
func newAssetPipeline(ed *edition.Edition) *assetPipeline {
	lim := ed.Limits()
	p := &assetPipeline{
		pres:         ed.Presentation(),
		hostedEvents: ed.Routes().HostedEvents,
		limits:       lim,
		limitsText:   newLimitsRewriter(lim),
	}
	// instanceLimits (limitstext.go) is a package fallback for the one path
	// with no assetPipeline of its own: the cached Claude plugin zip
	// (ui.go's buildPluginZip/copyRewritten), built once per process via
	// sync.Once. Every real process builds exactly one edition's pipeline,
	// so this assignment is that edition's own snapshot; only a test that
	// builds both editions in one process could see the second one win here,
	// and that zip is not edition-dependent content.
	instanceLimits = p.limitsText
	assetRegistry.mu.Lock()
	// The transitional startup settings (hack_mode.go) are the defaults.
	p.chromeHack.Store(hackChrome)
	p.setInkBase(hackInkBaseURL)
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

// inkBaseURL is the pipeline's hack instance address.
func (p *assetPipeline) inkBaseURL() string {
	if s := p.inkBase.Load(); s != nil {
		return *s
	}
	return ""
}

func (p *assetPipeline) setInkBase(base string) { p.inkBase.Store(&base) }

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
	eachAssetPipelineLocked(fn)
}

// eachAssetPipelineLocked is eachAssetPipeline for a caller holding the lock.
func eachAssetPipelineLocked(fn func(*assetPipeline)) {
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
	defer assetRegistry.mu.Unlock()
	hackChrome = on
	eachAssetPipelineLocked(func(p *assetPipeline) { p.chromeHack.Store(on) })
}

// SetHackInstanceURL is called once at startup, before routes or ZIP caches.
func SetHackInstanceURL(base string) {
	base = strings.TrimRight(base, "/")
	assetRegistry.mu.Lock()
	hackInkBaseURL = base
	eachAssetPipelineLocked(func(p *assetPipeline) { p.setInkBase(base) })
	assetRegistry.mu.Unlock()
	chromeCache.Clear()
}
