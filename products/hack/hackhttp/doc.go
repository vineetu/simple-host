// Package hackhttp holds Simple Hack's HTTP routes, all mounted in routes.go.
// Rules are in products/hack/rules (pure), SQL in products/hack/repository,
// workflows in products/hack/app; this package holds none of them and never
// imports a repository package.
//
// A handler decodes the request, checks the caller (loadMember), calls app
// functions and writes the JSON. The exceptions, kept here because they need
// the SiteHandler (h.links.hooks) only this package holds: views (entry list
// items, event views) put several app reads into one response, and siteLinks
// owns team-site addresses, the site-live check for result cards and the pin
// sweep that runs before a team list is read.
package hackhttp
