package handler

import "github.com/vsriram/simple-host/host/app/sites"

// SiteDisk combines the independently defined content and local serving contracts.
// The composition root supplies DiskStorage for both today.
type SiteDisk interface {
	sites.SiteFiles
	sites.LocalServing
}
