package servertest

import (
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
)

// withHosts gives a handler built by hand the address settings its fields
// name (NewSiteHandlers builds them for real handlers).
func WithHosts(h *siteshttp.SiteHandler) *siteshttp.SiteHandler {
	ed := h.Ed
	if ed == nil {
		ed = edition.Default(config.DefaultLimits())
	}
	h.Hosts = sites.NewHosts(h.Database, h.Disk, ed, h.SiteDomain, h.ContentHost, h.CNAMETarget, h.CustomDomainIP)
	return h
}
