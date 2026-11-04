package handler

import (
	"context"

	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/model"
)

func (h *SiteHandler) domainCertSvc() *sites.Service {
	return sites.New(h.database, nil, nil, 0, sites.Deps{DomainCerts: sites.DomainCertDeps{
		Partner: func(ctx context.Context, domain, siteID string) string {
			return h.requestablePartner(ctx, model.BoundDomain{Domain: domain, SiteID: siteID})
		},
		Allow: func(ctx context.Context, userID, domain string) (bool, error) {
			return h.sitesSvc().AllowDomainCertRequest(ctx, userID, domain, h.limits.DomainCertsDaily)
		},
	}})
}
