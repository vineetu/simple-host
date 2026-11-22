package domains

import (
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
)

// withHosts gives a bare route Handler the instance's Hosts, as start-up does.
func withHosts(sh *sitesroute.Handler) Handler {
	ed := sh.Ed
	if ed == nil {
		ed = edition.Default(config.DefaultLimits())
	}
	sh.Hosts = sites.NewHosts(sh.Database, sh.Disk, ed, sh.SiteDomain, sh.ContentHost, sh.CNAMETarget, sh.CustomDomainIP)
	return Handler{sh}
}
