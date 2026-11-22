// Package sitelist is the owner's and the operator's list of sites
// (GET /v1/sites): which sites are listed, the filter, order and paging,
// and what each row shows beside the site (views, footprint, pin, keep mark).
// It runs on the core site service; Register (routes.go) mounts the route.
package sitelist

import "github.com/vsriram/simple-host/host/sites"

// Service runs the list on the instance's site service.
type Service struct{ *sites.Service }
