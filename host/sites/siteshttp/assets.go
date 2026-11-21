package siteshttp

import (
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/web"
)

// pipeline is h's asset pipeline. NewSiteHandlers builds it; a bare
// &SiteHandler{} (tests) gets one for its edition on first use.
func (h *SiteHandler) Pipeline() *web.AssetPipeline {
	h.assetsOnce.Do(func() {
		if h.Assets == nil {
			h.Assets = web.NewAssetPipeline(h.Ed, "")
		}
	})
	return h.Assets
}

// Pages is h's asset pipeline, exported so moving Hack registrars
// (cleanup 3.2 slice 2a) can take the narrow hackPages interface instead
// of *SiteHandler itself, which they used to receive just to call this.
// Returns the concrete (unexported) *AssetPipeline, not host/hackhttp's
// hackPages interface (cleanup 3.2 slice 3: that interface moved there with
// hack.go, and handler may not import hackhttp): every hackPages method is
// exported on *AssetPipeline, so Go satisfies the interface implicitly at
// each call site, the same way NewPages already returns *AssetPipeline for
// test fixtures.
func (h *SiteHandler) Pages() *web.AssetPipeline { return h.Pipeline() }

// DiskForTest exposes only paths needed by cross-transport fixtures.
func (h *SiteHandler) DiskForTest() interface {
	SiteDir(string, string) string
	TrashDir(string, string) string
} {
	return h.Disk
}

// SetEditionForTest swaps h's edition after construction. Test hook
// (cleanup 3.2 slice 3): the moved hack_tls_test.go rebuilds a *SiteHandler
// as the ordinary Host edition partway through, to check that an
// unrecognised platform name still falls back to the small-box product's
// existing allowlist; ed is an unexported field.
func (h *SiteHandler) SetEditionForTest(ed *edition.Edition) {
	h.Hosts.SetEdition(ed)
	h.Ed = ed
}

// RebasePagesForTest rebuilds h's asset pipeline for instanceURL, keeping
// its edition. Test hook (cleanup 3.2 slice 3): the moved
// hack_ink_browser_test.go rebased a *SiteHandler's pipeline onto its test
// server's own address by assigning sh.assets directly before the move;
// that field is unexported, so a cross-package test needs this instead.
func (h *SiteHandler) RebasePagesForTest(instanceURL string) {
	h.Assets = web.NewAssetPipeline(h.Ed, instanceURL)
}
