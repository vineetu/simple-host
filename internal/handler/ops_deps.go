package handler

import (
	"github.com/vsriram/simple-host/internal/app/notifications"
	"github.com/vsriram/simple-host/internal/app/sites"
)

func (h *SiteHandler) notificationsSvc() *notifications.Service {
	return notifications.New(h.database, h.sitesSvc()).WithGroupSites(h.notificationGroups)
}

func (h *SiteHandler) opsSitesSvc() *sites.Service {
	return h.sitesSvc().WithPlatformDomains(h.storeAccounts().NamespaceDomains())
}

// SetNotificationGroups installs the product hook before serving requests.
func (h *SiteHandler) SetNotificationGroups(groups notifications.GroupSites) {
	h.notificationGroups = groups
}
