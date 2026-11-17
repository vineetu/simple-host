package handler

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"github.com/vsriram/simple-host/host/app/sites"
	"github.com/vsriram/simple-host/host/model"
)

// SiteFacts is the address/response boundary. Protection reads (including the
// visitor session, viewer membership and owner frame guard) stay outside it.
// The single process always uses postgresFacts. Only Static installs CopyFacts.
// Full model returns preserve existing callers; a copy supplies only routing
// fields and the two response-only projections, never protection fields.
type SiteFacts interface {
	GroupBySlug(context.Context, string) (GroupRef, error)
	GroupHasSite(context.Context, string, string) (bool, error)
	ContentPathOwner(ctx context.Context, handle, name string) (model.User, error)
	HostOwner(ctx context.Context, handle, name string) (model.User, error)
	GetUserByHandle(ctx context.Context, handle string) (model.User, error)
	GetUserByHandleOrAlias(ctx context.Context, handle string) (model.User, error)
	ResolveHandleAlias(ctx context.Context, handle string) (string, error)
	GetSiteByUser(ctx context.Context, userID, name string) (model.Site, error)
	AddressedSite(ctx context.Context, userID, name string) (model.Site, error)
	AddressedOldSite(ctx context.Context, userID, name string) (string, string, error)
	ResolveOldSiteName(ctx context.Context, userID, name string) (string, string, error)
	GetSiteOwner(ctx context.Context, siteID string) (string, string, string, error)
	GetSiteByCustomDomain(ctx context.Context, host string) (model.SiteDomainInfo, error)
	GetSiteDomainInfo(ctx context.Context, siteID string) (model.SiteDomainInfo, bool, error)
	GetRetiredName(ctx context.Context, host string) (model.RetiredName, error)
	DomainHeldByDeletedSite(ctx context.Context, host string) (bool, error)
	GetHandleBySiteName(ctx context.Context, name string) (string, error)
	HomeSite(ctx context.Context, userID string) (model.Site, bool, error)
	ListAllFamilies(ctx context.Context) ([]model.AddressFamily, error)
}

type postgresFacts struct{ h *SiteHandler }

func (p postgresFacts) ContentPathOwner(ctx context.Context, handle, name string) (model.User, error) {
	return p.h.sitesSvc().ContentPathOwner(ctx, handle, name)
}
func (p postgresFacts) HostOwner(ctx context.Context, handle, name string) (model.User, error) {
	return p.h.sitesSvc().HostOwner(ctx, handle, name)
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
	return p.h.sitesSvc().GetSiteByUser(ctx, userID, name)
}
func (p postgresFacts) AddressedSite(ctx context.Context, userID, name string) (model.Site, error) {
	return p.h.sitesSvc().AddressedSite(ctx, userID, name)
}
func (p postgresFacts) AddressedOldSite(ctx context.Context, userID, name string) (string, string, error) {
	return p.h.sitesSvc().AddressedOldSite(ctx, userID, name)
}
func (p postgresFacts) ResolveOldSiteName(ctx context.Context, userID, name string) (string, string, error) {
	return p.h.sitesSvc().ResolveOldSiteName(ctx, userID, name)
}
func (p postgresFacts) GetSiteOwner(ctx context.Context, siteID string) (string, string, string, error) {
	return p.h.sitesSvc().GetSiteOwner(ctx, siteID)
}
func (p postgresFacts) GetSiteByCustomDomain(ctx context.Context, host string) (model.SiteDomainInfo, error) {
	return p.h.sitesSvc().GetSiteByCustomDomain(ctx, host)
}
func (p postgresFacts) GetSiteDomainInfo(ctx context.Context, siteID string) (model.SiteDomainInfo, bool, error) {
	return p.h.sitesSvc().GetSiteDomainInfo(ctx, siteID)
}
func (p postgresFacts) GetRetiredName(ctx context.Context, host string) (model.RetiredName, error) {
	return p.h.sitesSvc().GetRetiredName(ctx, host)
}
func (p postgresFacts) DomainHeldByDeletedSite(ctx context.Context, host string) (bool, error) {
	return p.h.sitesSvc().DomainHeldByDeletedSite(ctx, host)
}
func (p postgresFacts) GetHandleBySiteName(ctx context.Context, name string) (string, error) {
	return p.h.sitesSvc().GetHandleBySiteName(ctx, name)
}
func (p postgresFacts) HomeSite(ctx context.Context, userID string) (model.Site, bool, error) {
	return p.h.sitesSvc().HomeSite(ctx, userID)
}
func (p postgresFacts) ListAllFamilies(ctx context.Context) ([]model.AddressFamily, error) {
	return p.h.sitesSvc().ListAllFamilies(ctx)
}

func (h *SiteHandler) siteFacts() SiteFacts {
	if h.routingFacts != nil {
		return h.routingFacts
	}
	return postgresFacts{h}
}

// SetSiteFacts is startup-only. The ordinary server does not call it.
func (h *SiteHandler) SetSiteFacts(f SiteFacts) { h.routingFacts = f }

// CopyFacts atomically publishes immutable tables. Missing facts return
// sql.ErrNoRows; the Static request boundary owns its one bounded miss lookup.
type CopyFacts struct{ current atomic.Pointer[copyFacts] }
type copyFacts struct{ table model.SiteTable }

func NewCopyFacts(t model.SiteTable) *CopyFacts { c := &CopyFacts{}; c.Replace(t); return c }
func (c *CopyFacts) Replace(t model.SiteTable)  { c.current.Store(&copyFacts{table: t}) }
func (c *CopyFacts) View() SiteFacts            { return c.current.Load() }

func (c *copyFacts) GetUserByHandle(_ context.Context, h string) (model.User, error) {
	for _, u := range c.table.Owners {
		if u.Handle == h && h != "" {
			return model.User{ID: u.ID, Handle: sql.NullString{String: h, Valid: true}}, nil
		}
	}
	return model.User{}, sql.ErrNoRows
}
func (c *copyFacts) ResolveHandleAlias(_ context.Context, h string) (string, error) {
	for _, a := range c.table.HandleAliases {
		if a.Name == strings.ToLower(h) {
			return a.Target, nil
		}
	}
	return "", sql.ErrNoRows
}
func (c *copyFacts) GetUserByHandleOrAlias(ctx context.Context, h string) (model.User, error) {
	u, e := c.GetUserByHandle(ctx, h)
	if !errors.Is(e, sql.ErrNoRows) {
		return u, e
	}
	alias, ae := c.ResolveHandleAlias(ctx, h)
	if ae != nil {
		return model.User{}, e
	}
	return c.GetUserByHandle(ctx, alias)
}
func (c *copyFacts) ContentPathOwner(ctx context.Context, handle, name string) (model.User, error) {
	if !sites.ContentPathValid(handle, name) {
		return model.User{}, sql.ErrNoRows
	}
	return c.GetUserByHandleOrAlias(ctx, handle)
}
func (c *copyFacts) HostOwner(ctx context.Context, h, n string) (model.User, error) {
	if !sites.HandleAddressable(h) || !sites.RoutingNameValid(n) {
		return model.User{}, sites.ErrInvalidHost
	}
	return c.GetUserByHandle(ctx, h)
}
func (c *copyFacts) site(r model.RouteSite) model.Site {
	var handle string
	for _, u := range c.table.Owners {
		if u.ID == r.UserID {
			handle = u.Handle
			break
		}
	}
	return model.Site{ID: r.ID, UserID: r.UserID, Name: r.Name, ActiveVersion: r.ActiveVersion, OwnerHandle: handle, PageNoIndex: r.RestrictedResponse, HomeUnavailable: r.HomeUnavailable}
}
func (c *copyFacts) GetSiteByUser(_ context.Context, u, n string) (model.Site, error) {
	for _, s := range c.table.Sites {
		if s.UserID == u && s.Name == n && !s.Deleted {
			return c.site(s), nil
		}
	}
	return model.Site{}, sql.ErrNoRows
}
func (c *copyFacts) AddressedSite(ctx context.Context, u, n string) (model.Site, error) {
	if !sites.RoutingNameValid(n) {
		return model.Site{}, sql.ErrNoRows
	}
	return c.GetSiteByUser(ctx, u, n)
}
func (c *copyFacts) ResolveOldSiteName(_ context.Context, u, n string) (string, string, error) {
	for _, a := range c.table.SiteAliases {
		if a.OwnerID == u && a.Name == n {
			for _, s := range c.table.Sites {
				if s.ID == a.Target && !s.Deleted && s.UserID == u {
					return s.ID, s.Name, nil
				}
			}
		}
	}
	return "", "", sql.ErrNoRows
}
func (c *copyFacts) AddressedOldSite(ctx context.Context, u, n string) (string, string, error) {
	if !sites.RoutingNameValid(n) {
		return "", "", sql.ErrNoRows
	}
	return c.ResolveOldSiteName(ctx, u, n)
}
func (c *copyFacts) GetSiteOwner(_ context.Context, id string) (string, string, string, error) {
	for _, s := range c.table.Sites {
		if s.ID == id && !s.Deleted {
			v := c.site(s)
			return v.OwnerHandle, s.UserID, s.Name, nil
		}
	}
	return "", "", "", sql.ErrNoRows
}
func (c *copyFacts) GetSiteDomainInfo(_ context.Context, id string) (model.SiteDomainInfo, bool, error) {
	for _, s := range c.table.Sites {
		if s.ID == id && s.Domain.Domain != "" {
			return s.Domain, true, nil
		}
	}
	return model.SiteDomainInfo{}, false, nil
}

// HostForms preserves the platform base aliases used by the existing query.
func (c *copyFacts) GetSiteByCustomDomain(_ context.Context, h string) (model.SiteDomainInfo, error) {
	f := sites.RoutingHostForms(h)
	for _, previous := range []bool{false, true} {
		for _, host := range f {
			for _, s := range c.table.Sites {
				if s.Deleted {
					continue
				}
				d := s.Domain.Domain
				if previous {
					d = s.Domain.PreviousDomain
				}
				if d == host && d != "" {
					if previous {
						return s.Domain.PreviousDomainView(time.Now()), nil
					}
					return s.Domain, nil
				}
			}
		}
	}
	return model.SiteDomainInfo{}, sql.ErrNoRows
}
func (c *copyFacts) GetRetiredName(_ context.Context, h string) (model.RetiredName, error) {
	for _, host := range sites.RoutingHostForms(h) {
		for _, r := range c.table.Retired {
			if r.Host == strings.ToLower(host) {
				return model.RetiredName{SiteID: r.SiteID}, nil
			}
		}
	}
	return model.RetiredName{}, sql.ErrNoRows
}
func (c *copyFacts) DomainHeldByDeletedSite(_ context.Context, h string) (bool, error) {
	for _, host := range sites.RoutingHostForms(h) {
		for _, s := range c.table.Sites {
			if s.Deleted && (s.Domain.Domain == strings.ToLower(host) || s.Domain.PreviousDomain == strings.ToLower(host)) {
				return true, nil
			}
		}
	}
	return false, nil
}
func (c *copyFacts) GetHandleBySiteName(_ context.Context, n string) (string, error) {
	// LoadSiteTable orders sites by creation time, matching the oldest-owner query.
	for _, s := range c.table.Sites {
		if !s.Deleted && s.Name == n {
			return c.site(s).OwnerHandle, nil
		}
	}
	return "", sql.ErrNoRows
}
func (c *copyFacts) HomeSite(_ context.Context, u string) (model.Site, bool, error) {
	for _, o := range c.table.Owners {
		if o.ID == u {
			for _, s := range c.table.Sites {
				if s.ID == o.HomeSiteID && !s.Deleted {
					return c.site(s), true, nil
				}
			}
			return model.Site{}, false, nil
		}
	}
	return model.Site{}, false, sql.ErrNoRows
}
func (c *copyFacts) ListAllFamilies(context.Context) ([]model.AddressFamily, error) {
	return append([]model.AddressFamily(nil), c.table.Families...), nil
}

var _ SiteFacts = postgresFacts{}
var _ SiteFacts = (*copyFacts)(nil)

func (c *CopyFacts) ContentPathOwner(ctx context.Context, handle, name string) (model.User, error) {
	return c.View().ContentPathOwner(ctx, handle, name)
}
func (c *CopyFacts) HostOwner(ctx context.Context, handle, name string) (model.User, error) {
	return c.View().HostOwner(ctx, handle, name)
}
func (c *CopyFacts) GetUserByHandle(ctx context.Context, handle string) (model.User, error) {
	return c.View().GetUserByHandle(ctx, handle)
}
func (c *CopyFacts) GetUserByHandleOrAlias(ctx context.Context, handle string) (model.User, error) {
	return c.View().GetUserByHandleOrAlias(ctx, handle)
}
func (c *CopyFacts) ResolveHandleAlias(ctx context.Context, handle string) (string, error) {
	return c.View().ResolveHandleAlias(ctx, handle)
}
func (c *CopyFacts) GetSiteByUser(ctx context.Context, userID, name string) (model.Site, error) {
	return c.View().GetSiteByUser(ctx, userID, name)
}
func (c *CopyFacts) AddressedSite(ctx context.Context, userID, name string) (model.Site, error) {
	return c.View().AddressedSite(ctx, userID, name)
}
func (c *CopyFacts) AddressedOldSite(ctx context.Context, userID, name string) (string, string, error) {
	return c.View().AddressedOldSite(ctx, userID, name)
}
func (c *CopyFacts) ResolveOldSiteName(ctx context.Context, userID, name string) (string, string, error) {
	return c.View().ResolveOldSiteName(ctx, userID, name)
}
func (c *CopyFacts) GetSiteOwner(ctx context.Context, siteID string) (string, string, string, error) {
	return c.View().GetSiteOwner(ctx, siteID)
}
func (c *CopyFacts) GetSiteByCustomDomain(ctx context.Context, host string) (model.SiteDomainInfo, error) {
	return c.View().GetSiteByCustomDomain(ctx, host)
}
func (c *CopyFacts) GetSiteDomainInfo(ctx context.Context, siteID string) (model.SiteDomainInfo, bool, error) {
	return c.View().GetSiteDomainInfo(ctx, siteID)
}
func (c *CopyFacts) GetRetiredName(ctx context.Context, host string) (model.RetiredName, error) {
	return c.View().GetRetiredName(ctx, host)
}
func (c *CopyFacts) DomainHeldByDeletedSite(ctx context.Context, host string) (bool, error) {
	return c.View().DomainHeldByDeletedSite(ctx, host)
}
func (c *CopyFacts) GetHandleBySiteName(ctx context.Context, name string) (string, error) {
	return c.View().GetHandleBySiteName(ctx, name)
}
func (c *CopyFacts) HomeSite(ctx context.Context, userID string) (model.Site, bool, error) {
	return c.View().HomeSite(ctx, userID)
}
func (c *CopyFacts) ListAllFamilies(ctx context.Context) ([]model.AddressFamily, error) {
	return c.View().ListAllFamilies(ctx)
}

// GroupFactsLoader optionally extends GroupHosts for a read-only table load.
// It uses the platform load's transaction, and returns address facts only.
type GroupFactsLoader interface {
	LoadRoutingFacts(context.Context, *sql.Tx) ([]model.RouteGroup, []model.RouteGroupSite, error)
}

func (h *SiteHandler) LoadSiteTable(ctx context.Context) (model.SiteTable, time.Time, error) {
	var load func(context.Context, *sql.Tx) ([]model.RouteGroup, []model.RouteGroupSite, error)
	if h.groupHosts != nil {
		g, ok := h.groupHosts.(GroupFactsLoader)
		if !ok {
			return model.SiteTable{}, time.Time{}, errors.New("group routing facts loader unavailable")
		}
		load = g.LoadRoutingFacts
	}
	return h.sitesSvc().LoadSiteTable(ctx, load)
}
func (p postgresFacts) GroupBySlug(ctx context.Context, s string) (GroupRef, error) {
	g, e := p.h.groupBySlug(ctx, s)
	return g.GroupRef, e
}
func (p postgresFacts) GroupHasSite(ctx context.Context, id, n string) (bool, error) {
	if p.h.groupHosts == nil {
		return false, nil
	}
	return p.h.groupHosts.HasSite(ctx, id, n)
}
func (c *copyFacts) GroupBySlug(_ context.Context, s string) (GroupRef, error) {
	for _, g := range c.table.Groups {
		if g.Slug == s {
			return GroupRef{ID: g.ID, Slug: g.Slug, AccountID: g.AccountID}, nil
		}
	}
	return GroupRef{}, sql.ErrNoRows
}
func (c *copyFacts) GroupHasSite(_ context.Context, id, n string) (bool, error) {
	for _, s := range c.table.GroupSites {
		if s.GroupID == id && s.Name == n {
			return true, nil
		}
	}
	return false, nil
}
func (c *CopyFacts) GroupBySlug(ctx context.Context, s string) (GroupRef, error) {
	return c.View().GroupBySlug(ctx, s)
}
func (c *CopyFacts) GroupHasSite(ctx context.Context, id, n string) (bool, error) {
	return c.View().GroupHasSite(ctx, id, n)
}

func (h *SiteHandler) LookupRoutingSite(ctx context.Context, address model.RoutingAddress) (model.SiteTable, error) {
	return h.sitesSvc().LookupRoutingSite(ctx, address)
}
