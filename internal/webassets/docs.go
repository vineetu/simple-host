package webassets

import (
	"embed"
	"io/fs"
	"sort"
	"strings"
)

// Docs is separate from Static: its files can only be served by the docs handler.
// all: includes Astro's underscore-prefixed bundles and Pagefind's assets.
//
//go:embed all:docs-site
var Docs embed.FS

func DocsTree(hack bool) fs.FS {
	brand := "host"
	if hack {
		brand = "hack"
	}
	return sub(Docs, "docs-site/"+brand)
}

// DocsPaths is the compiled brand's page list, also used by the sitemap.
func DocsPaths(hack bool) []string {
	var paths []string
	_ = fs.WalkDir(DocsTree(hack), ".", func(name string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && (name == "index.html" || strings.HasSuffix(name, "/index.html")) {
			paths = append(paths, "/docs/"+strings.TrimSuffix(name, "index.html"))
		}
		return err
	})
	sort.Strings(paths)
	return paths
}
