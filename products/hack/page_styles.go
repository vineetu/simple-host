package hack

import (
	"io/fs"

	"github.com/vsriram/simple-host/products/hack/web/dist"
	hackassets "github.com/vsriram/simple-host/products/hack/webassets"
)

// pageStyles are the two stylesheets the web app's build makes for the pages
// the server writes (web/hack/src/styles): the public event page's and the
// chrome pages' (Presentation StylePath). They are Hack's own file names, so
// they live beside the static files under the same "static/" names.
var pageStyles = map[string]string{
	"static/hack-event.css": "files/hack-event.css",
	"static/hack-pages.css": "files/hack-pages.css",
}

// productFiles is Hack's static files with the two built stylesheets
// answered from the web app's embedded build.
type productFiles struct{}

func (productFiles) Open(name string) (fs.File, error) {
	if built, ok := pageStyles[name]; ok {
		return dist.Files.Open(built)
	}
	return hackassets.Static.Open(name)
}

func (productFiles) ReadFile(name string) ([]byte, error) {
	if built, ok := pageStyles[name]; ok {
		return dist.Files.ReadFile(built)
	}
	return hackassets.Static.ReadFile(name)
}
