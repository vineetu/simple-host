package handler

import (
	"context"
	"github.com/vsriram/simple-host/internal/app/sites"
	db "github.com/vsriram/simple-host/internal/db"
)

func (h *SiteHandler) domainCertSvc() *sites.Service {
	return sites.New(h.database, nil, nil, 0, sites.Deps{DomainCerts: sites.DomainCertDeps{
		Partner: func(ctx context.Context, domain, siteID string) string {
			return h.requestablePartner(ctx, db.BoundDomain{Domain: domain, SiteID: siteID})
		},
		Allow: func(ctx context.Context, userID, domain string) (bool, error) {
			return db.AllowDomainCertRequest(ctx, h.database, userID, domain, h.limits.DomainCertsDaily)
		},
	}})
}
