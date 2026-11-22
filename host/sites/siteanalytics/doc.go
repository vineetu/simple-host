// Package siteanalytics holds the site analytics routes: a site's visits,
// countries and top pages and referrers, and the per-site summary. The
// numbers come from host/sites/analytics through host/ops' Reporting; the
// routes are not in host/sites/analytics because host/ops imports that
// package. Only siteshttp (the mount) imports this one.
package siteanalytics
