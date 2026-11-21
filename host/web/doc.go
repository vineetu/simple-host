// Package web is what every route package shares: the page and asset
// pipeline (AssetPipeline: chrome, static pages, skills, instance text), the
// instance-wide pages and middleware (health, CORS, security headers,
// notices, API errors) and the small HTTP refusals every module writes
// (shared_http.go, shared_pages.go). Plain request/response helpers belong
// in host/httpx instead.
//
// It imports no route package. A new shared helper goes here only when two
// route packages need it; a helper one module uses stays in that module's
// route package.
package web
