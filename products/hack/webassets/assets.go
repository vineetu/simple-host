// Package webassets holds Hack's own pages and skill sources.
package webassets

import "embed"

//go:embed static/hackathons.html
var Static embed.FS
