package siteshttp

import (
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/web"
)

// NewSiteHandlers builds the pipeline; a bare &SiteHandler{} (tests) gets one on first use.
func (h *SiteHandler) Pages() *web.AssetPipeline {
	h.assetsOnce.Do(func() {
		if h.Assets == nil {
			h.Assets = web.NewAssetPipeline(h.Ed, "")
		}
	})
	return h.Assets
}

func (h *SiteHandler) DiskForTest() interface {
	SiteDir(string, string) string
	TrashDir(string, string) string
} {
	return h.Disk
}

func (h *SiteHandler) SetEditionForTest(ed *edition.Edition) {
	h.Hosts.SetEdition(ed)
	h.Ed = ed
}

func (h *SiteHandler) RebasePagesForTest(instanceURL string) {
	h.Assets = web.NewAssetPipeline(h.Ed, instanceURL)
}
