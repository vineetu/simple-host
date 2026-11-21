package siteshttp

import (
	"context"
	"database/sql"

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/data/repo"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
)

func (h *SiteHandler) LifecycleSvc() *sites.Service { return h.lifecycleSvcWithTransaction(nil) }

// lifecycleSvcWithTransaction preserves the idle cleanup's companion write in the site transaction.
func (h *SiteHandler) lifecycleSvcWithTransaction(also func(*sql.Tx) error) *sites.Service {
	return sites.New(h.Database, h.Disk, h.PreviewAccounts, h.PreviewTTL, sites.Deps{
		GroupGate:          h.groupGate,
		DeletedTransaction: also,
		ResponseState: func(site model.Site, version int, preview, footprint bool) any {
			return h.captureSiteResponseState(site, version, preview, footprint)
		},
		LockSite: h.LockSite, DeletedRetention: h.Limits.DeletedRetention,
		HasLiveFamilies: h.Hosts.HasLiveFamilies, SyncRedirect: h.Hosts.SyncDomainRedirect,
		RequestCert: h.RequestSiteCert, CancelDomainCert: h.Hosts.CancelDomainCert,
		AdmitSQLite: func(ctx context.Context, siteID string, fn func(context.Context) error) error {
			return h.SqliteSites.Admit(ctx, siteID, func(ctx context.Context, _ *appstorage.Admission) error { return fn(ctx) })
		},
		ExportResources: func(ctx context.Context, siteID string) ([]map[string]any, error) {
			list, err := repo.ListResources(ctx, h.Database, siteID)
			if err != nil {
				return nil, err
			}
			resources := []map[string]any{}
			for _, x := range list {
				resources = append(resources, appstorage.ResourceRulesJSON(x, nil))
			}
			return resources, nil
		},
	})
}
