package accesshttp

import (
	"github.com/vsriram/simple-host/host/web"
)

// The handlers below render pages too. main hands each the SiteHandler's
// pipeline once, before serving, so every page of an instance uses the same
// edition and Hack instance address: the one captured before a setup-saved
// domain replaces PublicBaseURL, as the startup setters did. A handler that
// was never given one (tests) builds its own from its edition.

// ShareAssets makes h render through the site handler's asset pipeline (SiteHandler.Pages).
func (h *UserHandler) ShareAssets(p *web.AssetPipeline) { h.assets = p }

// ShareAssets makes h render through the site handler's asset pipeline (SiteHandler.Pages).
func (h *OAuthHandler) ShareAssets(p *web.AssetPipeline) { h.assets = p }

// ShareAssets makes h render through the site handler's asset pipeline (SiteHandler.Pages).
func (h *ConnectorHandler) ShareAssets(p *web.AssetPipeline) { h.assets = p }
