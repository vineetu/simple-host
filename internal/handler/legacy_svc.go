package handler

import (
	"context"
	"github.com/vsriram/simple-host/internal/app/legacy"
	"net/http"
)

func (h *SiteHandler) legacySvc() *legacy.Adapter {
	return legacy.New(h.database, h.legacyStorageCutoff, legacy.Deps{MaxEntries: h.savedData.EntriesMax, WatchItemKB: h.savedData.WatchItemKB, MaxBytes: h.siteMaxBytes(), NewListMetric: watchNewList, LargeItemMetric: watchItemLarge})
}

// legacyRequestSvc supplies the request-specific named-site resolver.
func (h *SiteHandler) legacyRequestSvc(r *http.Request) *legacy.Adapter {
	return legacy.New(h.database, h.legacyStorageCutoff, legacy.Deps{
		AdminAPIKey: h.adminAPIKey, KeyIdleExpiry: h.limits.KeyIdleExpiry,
		ResolveNamedSite: func(_ context.Context, siteName string) (string, error) { return h.resolveSiteID(r, siteName) },
	})
}
