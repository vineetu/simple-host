package handler

import "github.com/vsriram/simple-host/internal/app/sites"

func (h *SiteHandler) certRequestSvc() *sites.Service {
	return sites.New(h.database, h.disk, nil, 0, sites.Deps{CertRequests: sites.CertRequestDeps{
		SiteHostsOn: h.siteHostsOn, HandleAddressable: handleAddressable, Bases: h.certRequestBases,
		Ready: h.siteCertReadyOn, Directory: h.certDirFor, PersonHost: h.personHostOn,
	}})
}
