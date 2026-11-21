package siteshttp

import (
	"context"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/web"
)

// postgresFacts answers address and response lookups from the database.
// Protection reads (including the visitor session, viewer membership and
// owner frame guard) stay outside it.
type postgresFacts struct{ h *SiteHandler }

func (p postgresFacts) ContentPathOwner(ctx context.Context, handle, name string) (model.User, error) {
	return p.h.SitesSvc().ContentPathOwner(ctx, handle, name)
}
func (p postgresFacts) HostOwner(ctx context.Context, handle, name string) (model.User, error) {
	return p.h.SitesSvc().HostOwner(ctx, handle, name)
}
func (p postgresFacts) GetUserByHandle(ctx context.Context, handle string) (model.User, error) {
	return p.h.storeAccounts().GetUserByHandle(ctx, handle)
}
func (p postgresFacts) GetUserByHandleOrAlias(ctx context.Context, handle string) (model.User, error) {
	return p.h.storeAccounts().GetUserByHandleOrAlias(ctx, handle)
}
func (p postgresFacts) ResolveHandleAlias(ctx context.Context, handle string) (string, error) {
	return p.h.storeAccounts().ResolveHandleAlias(ctx, handle)
}
func (p postgresFacts) GetSiteByUser(ctx context.Context, userID, name string) (model.Site, error) {
	return p.h.SitesSvc().GetSiteByUser(ctx, userID, name)
}
func (p postgresFacts) AddressedSite(ctx context.Context, userID, name string) (model.Site, error) {
	return p.h.SitesSvc().AddressedSite(ctx, userID, name)
}
func (p postgresFacts) AddressedOldSite(ctx context.Context, userID, name string) (string, string, error) {
	return p.h.SitesSvc().AddressedOldSite(ctx, userID, name)
}
func (p postgresFacts) ResolveOldSiteName(ctx context.Context, userID, name string) (string, string, error) {
	return p.h.SitesSvc().ResolveOldSiteName(ctx, userID, name)
}
func (p postgresFacts) GetSiteOwner(ctx context.Context, siteID string) (string, string, string, error) {
	return p.h.SitesSvc().GetSiteOwner(ctx, siteID)
}
func (p postgresFacts) GetSiteByCustomDomain(ctx context.Context, host string) (model.SiteDomainInfo, error) {
	return p.h.SitesSvc().GetSiteByCustomDomain(ctx, host)
}
func (p postgresFacts) GetSiteDomainInfo(ctx context.Context, siteID string) (model.SiteDomainInfo, bool, error) {
	return p.h.SitesSvc().GetSiteDomainInfo(ctx, siteID)
}
func (p postgresFacts) GetRetiredName(ctx context.Context, host string) (model.RetiredName, error) {
	return p.h.SitesSvc().GetRetiredName(ctx, host)
}
func (p postgresFacts) DomainHeldByDeletedSite(ctx context.Context, host string) (bool, error) {
	return p.h.SitesSvc().DomainHeldByDeletedSite(ctx, host)
}
func (p postgresFacts) GetHandleBySiteName(ctx context.Context, name string) (string, error) {
	return p.h.SitesSvc().GetHandleBySiteName(ctx, name)
}
func (p postgresFacts) HomeSite(ctx context.Context, userID string) (model.Site, bool, error) {
	return p.h.SitesSvc().HomeSite(ctx, userID)
}
func (p postgresFacts) ListAllFamilies(ctx context.Context) ([]model.AddressFamily, error) {
	return p.h.SitesSvc().ListAllFamilies(ctx)
}

func (h *SiteHandler) siteFacts() postgresFacts { return postgresFacts{h} }

func (p postgresFacts) GroupBySlug(ctx context.Context, s string) (web.GroupRef, error) {
	g, e := p.h.Hosts.GroupBySlug(ctx, s)
	return g.GroupRef, e
}
func (p postgresFacts) GroupHasSite(ctx context.Context, id, n string) (bool, error) {
	if g := p.h.Hosts.Groups(); g != nil {
		return g.HasSite(ctx, id, n)
	}
	return false, nil
}
