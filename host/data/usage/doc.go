// Package usage is the owner's storage usage report (GET
// /v1/sites/{sitename}/storage/usage): bytes used and left in the site's
// pool and its file pool. Measuring and the limits themselves are the data
// core's (usage.go there), because every write checks them.
package usage
