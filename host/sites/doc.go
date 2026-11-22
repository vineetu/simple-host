// Package sites is the Sites core: the site service (Service, whose fields
// the feature packages embed), sites.Hosts (addressing and host resolution
// shared by serving, Access and Data), the door look-ups, the site locks and
// the gate storage Access reads. Each feature is its own package beside it
// (deploy, versions, domains, lifecycle, events, families, sitelist,
// sitemap, export); the core never imports one, and other modules only ever
// need the core. It may import access (and calls it
// directly for limits, retention, handles and viewers), never data: a step
// that touches saved data after a Sites call (events, push devices, an
// export with SQLite) is done by host/ops (the domain and family check
// workers in siteshttp still emit their events through data/notify).
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
// Deps holds configuration and concrete collaborators only; the hook it
// keeps (Sitemap) is named with its reason in scripts/layering/funcfields.txt.
// Its HTTP routes are in siteshttp (routes.go), which composes the features.
package sites
