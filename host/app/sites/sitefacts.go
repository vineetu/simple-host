package sites

import (
	"context"
	"database/sql"
	"time"

	"github.com/vsriram/simple-host/host/model"
	accounts "github.com/vsriram/simple-host/host/repository/accounts"
	repo "github.com/vsriram/simple-host/host/repository/sites"
	rules "github.com/vsriram/simple-host/host/rules/sites"
)

func (s *Service) LoadSiteTable(ctx context.Context, groups func(context.Context, *sql.Tx) ([]model.RouteGroup, []model.RouteGroupSite, error)) (model.SiteTable, time.Time, error) {
	return repo.LoadSiteTable(ctx, s.database, groups)
}

func RoutingHostForms(host string) [2]string { return accounts.HostForms(host) }
func RoutingNameValid(name string) bool      { return rules.ValidSiteName.MatchString(name) }

func (s *Service) LookupRoutingSite(ctx context.Context, address model.RoutingAddress) (model.SiteTable, error) {
	return repo.LookupRoutingSite(ctx, s.database, address, accounts.HostForms(address.Domain))
}
