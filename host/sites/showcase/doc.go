// Package showcase is a person's public showcase and home page settings: the
// public showcase feed (GET /v1/u/{handle}/showcase.json), the bio, a site's
// pin and order on the showcase, and which site is the person's home page.
// Rendering the showcase and home page at a host is serving (siteshttp); the
// data both read is sitesroute.Handler.PublicShowcaseData.
package showcase
