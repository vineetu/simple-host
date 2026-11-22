// Package siteshttp mounts Sites' routes and serves sites at their own hosts.
//
// routes.go (RegisterRoutes) calls each feature's Register in order
// (deploy, lifecycle, showcase, sitelist, admin, versions, siteanalytics,
// export, idle, domains, families); a new route goes in its feature's
// routes.go, beside its neighbours, e.g. host/sites/versions/routes.go.
// The shared route state and helpers are host/sites/sitesroute.
//
// What stays here is the serving cluster (site, person, family, legacy,
// group and platform-subdomain hosts, the content-host redirects, preview
// links at a host, the not-found, offline and passcode pages, _redirects),
// the dashboard pages (ui.go), the site worker loop (site_workers.go) and
// the held-projects export. Visitors pass Access's gate (the Gate field, set
// by host/handler). It imports the modules, host/web and host/httpx, never
// another route package or a repo package.
package siteshttp
