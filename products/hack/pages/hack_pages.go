package pages

import (
	"io/fs"
	"net/http"
	"time"
)

// Pages is what Hack pages need of the site's asset/chrome pipeline (*assetPipeline in host/handler, which this package may not import).
type Pages interface {
	InkBaseURL() string
	// IsCanonicalInstance reports whether this is simple-hack.app itself, not a self-hosted instance.
	IsCanonicalInstance() bool
	StaticFS() fs.FS
	SkillsModTime() time.Time
	ServeStaticPage(name string) http.Handler
	// ChromePage is one embedded static/<name> with its chrome, before the script nonce is stamped.
	ChromePage(r *http.Request, name string) ([]byte, error)
	RenderPage(r *http.Request, name string, raw []byte) ([]byte, error)
	AdminUICSP(next http.Handler) http.Handler
	UICSP(next http.Handler, frameSrc string) http.Handler
	// RenderAppShell is static/hack/app.html with its chrome applied; the sign-in script tag only when signin is true.
	RenderAppShell(r *http.Request, body []byte, signin bool) ([]byte, error)
	LandingMarkdownHandler(file, title, description, publicBaseURL string, hackAssets bool) http.HandlerFunc
	WriteMessagePage(w http.ResponseWriter, r *http.Request, base string, status int, message, subtext, backURL, backLabel, form string)
	SkillServedText(path string, data []byte) []byte
	RegisterSkillsHub(mux *http.ServeMux, publicBaseURL string)
}
