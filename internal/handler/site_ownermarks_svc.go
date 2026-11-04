package handler

import "github.com/vsriram/simple-host/internal/app/sites"

func (h *SiteHandler) ownerMarkSvc() *sites.Service {
	return sites.New(h.database, nil, nil, 0, sites.Deps{OwnerMarks: sites.OwnerMarkDeps{Mark: h.ownerMarks.Mark}})
}
