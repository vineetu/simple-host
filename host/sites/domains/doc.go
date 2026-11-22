// Package domains is Sites' custom domains: binding a domain to a site,
// dropping, lapsing and promoting it, the issuer's certificate requests for
// it (and its www / bare partner), and the domain shape rule. It works on the
// core's sites.Service; the domain columns stay in the core's repo, which
// owns the sites table. The domain-check worker and the routes that call it
// live in host/sites/siteshttp.
package domains

import "github.com/vsriram/simple-host/host/sites"

// Service runs the domain workflows on the instance's site service.
type Service struct{ *sites.Service }
