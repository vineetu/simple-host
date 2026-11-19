// Package webassets holds Hack's own pages and skill sources.
package webassets

import "embed"

//go:embed all:static
var Static embed.FS
