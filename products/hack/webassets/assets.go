// Package webassets holds Hack's own pages and skill sources.
package webassets

import "embed"

//go:embed all:static all:docs-site
var Static embed.FS
