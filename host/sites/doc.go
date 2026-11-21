// Package sites owns site publication and lifecycle: deploys, versions,
// domains, addresses and analytics. It may import access (and calls it
// directly for limits, retention, handles and viewers), never data: a step
// that touches saved data after a Sites call (events, push devices, an
// export with SQLite) is done by the routes or by host/ops.
//
// Its door: repo/door.go and repo/door_access.go (the sites table's only
// reader and writer), product.go (what a product reads of sites) and the
// Service and Hosts methods.
//
// Its rules, one function each:
//   - site state: model.Site.Suspended, Live, Gated, OpenToAnyone and
//     HasVersion, with their SQL forms once in repo/site_state.go;
//   - the address to hand out: LiveURL and Addressing.SiteURLFor;
//   - which site a host or name means: Hosts.ResolveSite;
//   - the site name: host/rules/sites.ValidateShape and ValidateReserved.
//
// Deps holds configuration and concrete collaborators only; the two hooks it
// keeps are named with their reasons in scripts/layering/funcfields.txt.
// Its HTTP routes are in siteshttp (routes.go).
package sites
