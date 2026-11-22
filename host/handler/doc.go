// Package handler is where the three route packages meet: NewSiteHandlers
// builds the site, access and data handlers with the state they share, and
// Register mounts their routes, handing Access's gates to Data and the shared
// rate limiters to Sites and Access. The connector's visitor tools
// (visitor_tools.go) admit a visitor through Access and read or write the
// site's data through Data's routes, so they live here too.
//
// Nothing else belongs here: a route goes in its module's route package
// (host/access/accesshttp, host/data and its feature packages, host/sites/siteshttp).
package handler
