// Package ops holds the workflows that span access, sites and data, and the
// operator's reports over them. It may import all three modules; none of
// them imports it. Each workflow calls each module's own functions, on one
// transaction where the change must be atomic: cutting a person off
// (person.go), removing a named viewer with their push devices
// (site_viewers.go), a site's export with its saved data (site_archive.go),
// idle sweeps, pruning the operator's sites, usage and analytics.
package ops
