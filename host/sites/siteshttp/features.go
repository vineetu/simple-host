package siteshttp

import (
	"github.com/vsriram/simple-host/host/sites/deploy"
	"github.com/vsriram/simple-host/host/sites/export"
)

// deploys is the publishing workflow (host/sites/deploy) on the site service.
func (h *SiteHandler) deploys() deploy.Service { return deploy.Service{Service: h.SitesSvc()} }

// exports resolves export and team-preview sites on the smaller lifecycle service, as before.
func (h *SiteHandler) exports() export.Service { return export.Service{Service: h.LifecycleSvc()} }
