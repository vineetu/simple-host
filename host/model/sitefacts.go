package model

import "time"

// SiteTable contains address and response facts only. None of these records
// may authorize a file read. Access markers and fresh protection reads do that.
// RestrictedResponse is only the noindex response bit; HomeUnavailable only
// selects the person-page fallback. Neither represents an access verdict.
type SiteTable struct {
	Groups        []RouteGroup
	GroupSites    []RouteGroupSite
	Owners        []RouteOwner
	Sites         []RouteSite
	HandleAliases []RouteAlias
	SiteAliases   []RouteAlias
	Retired       []RouteRetired
	Families      []AddressFamily
}
type RouteOwner struct{ ID, Handle, HomeSiteID string }
type RouteSite struct {
	ID, UserID, Name                    string
	CreatedAt                           time.Time
	ActiveVersion                       int
	Deleted                             bool
	RestrictedResponse, HomeUnavailable bool
	Domain                              SiteDomainInfo
}
type RouteAlias struct{ OwnerID, Name, Target string }
type RouteRetired struct{ Host, SiteID string }

type RouteGroup struct{ ID, Slug, AccountID string }
type RouteGroupSite struct{ GroupID, Name string }

// RoutingAddress is a parsed page address: an owner/name pair or an exact
// hostname (custom domain or family). Family discovery can happen on a miss;
// label validation and certificate readiness remain in handler.
type RoutingAddress struct{ Handle, Name, Domain string }
