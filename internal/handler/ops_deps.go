package handler

import (
	"github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/app/notifications"
	"github.com/vsriram/simple-host/internal/app/sites"
)

func (h *SiteHandler) notificationsSvc() *notifications.Service {
	return notifications.New(h.database, h.sitesSvc())
}
func (h *SiteHandler) opsAccountsSvc() *accounts.Service { return accounts.New(h.database) }

func (h *SiteHandler) opsSitesSvc() *sites.Service {
	return h.sitesSvc().WithPlatformDomains(h.opsAccountsSvc().NamespaceDomains())
}
