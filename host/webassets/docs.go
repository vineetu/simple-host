package webassets

import (
	"embed"
	"io/fs"
)

// Docs is separate from Static: its files can only be served by the docs handler.
// all: includes Astro's underscore-prefixed bundles and Pagefind's assets.
//
//go:embed all:docs-site
var Docs embed.FS

// DocsTree is the platform's compiled docs. A product's docs live with the
// product and reach the docs handler through its asset contribution.
func DocsTree() fs.FS {
	return sub(Docs, "docs-site/host")
}
