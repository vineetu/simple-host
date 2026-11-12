package handler

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/model"
)

func (h *SiteHandler) lifecycleSvc() *sites.Service { return h.lifecycleSvcWithTransaction(nil) }

// lifecycleSvcWithTransaction preserves the idle cleanup's companion write in the site transaction.
func (h *SiteHandler) lifecycleSvcWithTransaction(also func(*sql.Tx) error) *sites.Service {
	return sites.New(h.database, h.disk, h.previewAccounts, h.previewTTL, sites.Deps{
		DeletedTransaction: also,
		ResponseState: func(site model.Site, version int, preview, footprint bool) any {
			return h.captureSiteResponseState(site, version, preview, footprint)
		},
		LockSite: h.lockSite, DeletedRetention: h.limits.DeletedRetention,
		ImageScanEnabled: h.imageScan != nil,
		HasLiveFamilies:  h.hasLiveFamilies, SyncRedirect: h.syncDomainRedirect,
		RequestCert: h.RequestSiteCert, CancelDomainCert: h.cancelDomainCert,
		ExportSQLiteMaxMemory: storageSQLiteMaxMemory,
		ExportResources: func(ctx context.Context, siteID string) ([]map[string]any, error) {
			list, err := h.listStorageResourceRows(ctx, siteID)
			if err != nil {
				return nil, err
			}
			resources := []map[string]any{}
			for _, x := range list {
				resources = append(resources, storageResourceJSON(x))
			}
			return resources, nil
		},
	})
}
