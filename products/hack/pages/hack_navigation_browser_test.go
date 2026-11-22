package pages

import (
	"io/fs"
	"net/http"

	"github.com/vsriram/simple-host/products/hack/webassets"
)

type navigationBrowserPages struct{ Pages }

func (navigationBrowserPages) ServeStaticPage(name string) http.Handler {
	assets, _ := fs.Sub(webassets.Static, "static")
	return http.FileServer(http.FS(assets))
}
func (navigationBrowserPages) StaticFS() fs.FS                                { return webassets.Static }
func (navigationBrowserPages) AdminUICSP(next http.Handler) http.Handler      { return next }
func (navigationBrowserPages) UICSP(next http.Handler, _ string) http.Handler { return next }
func (navigationBrowserPages) RenderAppShell(_ *http.Request, body []byte, _ bool) ([]byte, error) {
	return body, nil
}
