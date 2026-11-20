package handler

import (
	notifications "github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/sites"
)

func (h *SiteHandler) notificationsSvc() *notifications.Service {
	return notifications.New(h.database, h.sitesSvc())
}

func (h *SiteHandler) opsSitesSvc() *sites.Service {
	return h.sitesSvc().WithPlatformDomains(h.storeAccounts().NamespaceDomains())
}
