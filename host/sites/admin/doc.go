// Package admin is the operator's reports on sites: the paged site list
// (GET /v1/admin/sites), every account with its sites (GET /v1/admin/users)
// and the instance's disk, network and worker usage (GET /v1/admin/usage).
// The reports themselves are host/ops's (Reporting); this package reads the
// request, asks ops and shapes the answer on the shared route state.
package admin
