package handler

import (
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"
	"weak"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/edition"
)

// canonicalHackBaseURL is simple-hack.app's own address: the hack ink base
// every Hack instance starts from unless it is given its own (the
// instanceURL constructor argument to newAssetPipeline).
const canonicalHackBaseURL = "https://simple-hack.app"

// assetPipeline is everything the static asset and chrome pipeline reads that
// depends on the product or the instance: which product's assets and copy it
// serves, the hack chrome, the hack instance's own address, and the cache of
// swapped embedded files. A SiteHandler owns one (h.assets), built from its
// Edition in NewSiteHandler; the pipeline's methods read only these fields.
// Every field below is set once, in newAssetPipeline, and never changed: the
// pipeline is immutable after construction.
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
	// chromeHack renders the simple-hack.app header, footer and ink: always
	// pres.HackAssets, kept as its own field because that is the question
	// this pipeline's callers ask.
	chromeHack bool
	// inkBase is this Hack instance's public address (the instanceURL
	// constructor argument), canonicalHackBaseURL by default. Read via
	// inkBaseURL. Meaningless when !chromeHack.
	inkBase string
	// text holds swapped copies of embedded files, by base and name
	// (baseTextFS.Open).
	text sync.Map

	// skillsZip, pluginZip and hackSkillsZip are this instance's cached zip
	// downloads (ui.go, hack_get_started.go), each built once.
	skillsZip     cachedZip
	pluginZip     cachedZip
	hackSkillsZip cachedZip
}

// cachedZip is a sync.Once-guarded zip build: bytes on success, err on
// failure, cached either way so a later call does not redo the walk.
type cachedZip struct {
	once  sync.Once
	bytes []byte
	err   error
}

// newAssetPipeline builds the pipeline for ed (nil = Host) and its Hack
// instance address ("" = canonicalHackBaseURL, the default every Hack
// instance starts from unless it is self-hosted under its own domain), and
// registers it so the base-text setters, which have no handler in reach,
// still reach its swapped-file cache.
func newAssetPipeline(ed *edition.Edition, instanceURL string) *assetPipeline {
	if instanceURL == "" {
		instanceURL = canonicalHackBaseURL
	}
	lim := ed.Limits()
	p := &assetPipeline{
		pres:         ed.Presentation(),
		hostedEvents: ed.Routes().HostedEvents,
		limits:       lim,
		limitsText:   newLimitsRewriter(lim),
		chromeHack:   ed.Presentation().HackAssets,
		inkBase:      strings.TrimRight(instanceURL, "/"),
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

// inkBaseURL is the pipeline's hack instance address.
func (p *assetPipeline) inkBaseURL() string { return p.inkBase }

// InkBaseURL is inkBaseURL, exported for the hackPages interface (hack.go):
// moving Hack code reaches the pipeline only through that interface, whose
// methods must be exported (cleanup 3.2 slice 2a).
func (p *assetPipeline) InkBaseURL() string { return p.inkBaseURL() }

// IsCanonicalInstance reports whether this is simple-hack.app itself, not a
// self-hosted or otherwise addressed instance. Exported for hackPages.
func (p *assetPipeline) IsCanonicalInstance() bool { return p.inkBaseURL() == canonicalHackBaseURL }

// StaticFS is the embedded static/ tree as this pipeline serves it
// (staticFS, below), exported for hackPages: moving code parses its own
// templates/pages from it instead of naming ui.go's staticFiles directly.
func (p *assetPipeline) StaticFS() fs.FS { return p.staticFS() }

// EmbeddedStatic is ui.go's staticFiles (the embedded tree, product-
// neutral), exported for moving code (cleanup 3.2 slice 2a) that has no
// pipeline in reach at the point it needs it, e.g. hack_eventpage.go's
// package-level hack-event.html template parse.
func EmbeddedStatic() fs.FS { return staticFiles }

// SkillsModTime is skillsModTime (ui.go), exported for hackPages: the one
// Last-Modified every skills/plugin download serves (set once at process
// start).
func (p *assetPipeline) SkillsModTime() time.Time { return skillsModTime }

// ServeStaticPage is serveStaticPage (ui.go), exported for hackPages.
func (p *assetPipeline) ServeStaticPage(name string) http.Handler { return p.serveStaticPage(name) }

// AdminUICSP is adminUICSP (ui.go), exported for hackPages.
func (p *assetPipeline) AdminUICSP(next http.Handler) http.Handler { return p.adminUICSP(next) }

// UICSP is uiCSP (ui.go), exported for hackPages.
func (p *assetPipeline) UICSP(next http.Handler, frameSrc string) http.Handler {
	return p.uiCSP(next, frameSrc)
}

// NewPages builds a pipeline for ed (nil = Host) and its Hack instance
// address ("" = canonicalHackBaseURL), exported for moving Hack code
// (cleanup 3.2 slice 2a) that has no SiteHandler.Pages() to call: the
// pipeline()/self-construct fallback a bare &HackHandler{} (most tests)
// falls back to when ShareAssets was never called.
func NewPages(ed *edition.Edition, instanceURL string) *assetPipeline {
	return newAssetPipeline(ed, instanceURL)
}

// rewritten reports whether the text assets (llms.txt, the spec, ...) need a
// rewriting handler rather than the plain file server.
func (p *assetPipeline) rewritten() bool { return instanceHosts != nil || p.limitsText != nil }

// pipeline is h's asset pipeline. NewSiteHandler builds it; a bare
// &SiteHandler{} (tests) gets one for its edition on first use.
func (h *SiteHandler) pipeline() *assetPipeline {
	h.assetsOnce.Do(func() {
		if h.assets == nil {
			h.assets = newAssetPipeline(h.ed, "")
		}
	})
	return h.assets
}

// Pages is h's asset pipeline, exported so moving Hack registrars
// (cleanup 3.2 slice 2a) can take the narrow hackPages interface instead
// of *SiteHandler itself, which they used to receive just to call this.
func (h *SiteHandler) Pages() hackPages { return h.pipeline() }

// staticFS is the embedded tree as this pipeline serves it.
func (p *assetPipeline) staticFS() baseTextFS {
	return baseTextFS{fsys: embeddedStatic, p: p}
}

// assetRegistry tracks every live pipeline, weakly, so the base-text setters
// (setBaseText, SetSiteBaseText), which have no handler in reach, can still
// clear every instance's swapped-file cache.
var assetRegistry struct {
	mu   sync.Mutex
	live []weak.Pointer[assetPipeline]
}

// clearAssetText drops every live pipeline's swapped-file cache.
func clearAssetText() {
	assetRegistry.mu.Lock()
	defer assetRegistry.mu.Unlock()
	for _, wp := range assetRegistry.live {
		if p := wp.Value(); p != nil {
			p.text.Clear()
		}
	}
}

// The handlers below render pages too. main hands each the SiteHandler's
// pipeline once, before serving, so every page of an instance uses the same
// edition and Hack instance address: the one captured before a setup-saved
// domain replaces PublicBaseURL, as the startup setters did. A handler that
// was never given one (tests) builds its own from its edition.

// ShareAssets makes h render through sh's asset pipeline.
func (h *UserHandler) ShareAssets(sh *SiteHandler) { h.assets = sh.pipeline() }

// ShareAssets makes h render through sh's asset pipeline.
func (h *OAuthHandler) ShareAssets(sh *SiteHandler) { h.assets = sh.pipeline() }

// ShareAssets makes h render through sh's asset pipeline.
func (h *ConnectorHandler) ShareAssets(sh *SiteHandler) { h.assets = sh.pipeline() }

// HackHandler's ShareAssets moved to hack.go (cleanup 3.2 slice 1c): a
// method with a *HackHandler receiver must live in the same package as
// that type, which moves to internal/hackhttp.
