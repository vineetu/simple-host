// Package dist is Simple Hack's new web app (web/hack at the repo root, built
// by `make hack-web`), committed so the Go build needs no Node. files/
// holds index.html, screens.json (the screens it serves) and hack/app/*.
package dist

import "embed"

//go:embed files
var Files embed.FS
