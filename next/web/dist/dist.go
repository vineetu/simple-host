// Package dist is the built owner app (../web/host at the repo root, built by `make web`) and the
// shared stylesheet for the pages the server writes itself. The files are
// committed, so the service builds without Node. Do not edit files/ by hand.
package dist

import (
	"embed"
	"io/fs"
)

//go:embed files
var files embed.FS

// Files is the build: index.html, sw.js, manifest.webmanifest, pages.css and app/.
var Files, _ = fs.Sub(files, "files")

// PagesCSS is the stylesheet of the gate, status and public text pages.
var PagesCSS = mustRead("pages.css")

// CaveatFont is the hand font (Caveat Bold, Latin), which a gate page carries inline.
var CaveatFont = mustRead("app/fonts/caveat-bold-latin.woff2")

func mustRead(name string) string {
	b, err := fs.ReadFile(Files, name)
	if err != nil {
		panic("web/dist: " + name + " is missing; run make web")
	}
	return string(b)
}
