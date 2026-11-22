// Package families is the address-families feature of Sites: an account
// connects *.<suffix> once and its sites also answer under it. It binds and
// verifies a family, changes its settings and certificate mode, releases it,
// and keeps the family issuer's request in step. The routes and the check
// worker that call it are in host/sites/siteshttp (familyapi.go); serving a
// family host is core state (sites.Hosts).
package families

import "github.com/vsriram/simple-host/host/sites"

// Service runs the family workflows on the site service.
type Service struct{ *sites.Service }
