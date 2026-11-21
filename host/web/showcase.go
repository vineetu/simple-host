package web

import (
	"encoding/json"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
)

type ShowcaseSite struct {
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	CreatedAt   time.Time `json:"created_at"`
	Visibility  string    `json:"visibility"`
	Title       string    `json:"title,omitempty"`
	Description string    `json:"description,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
	Pinned      bool      `json:"pinned"`
	Order       int       `json:"order"`
}

type ShowcaseData struct {
	Handle            string         `json:"handle"`
	Bio               string         `json:"bio"`
	SitesBaseURL      string         `json:"sitesBaseUrl"`
	PublicShowcaseURL string         `json:"publicShowcaseUrl"`
	OwnerAppURL       string         `json:"ownerAppUrl"`
	MainURL           string         `json:"mainUrl"`
	Sites             []ShowcaseSite `json:"sites"`
}

// ShowcasePage assembles the showcase template: the shared chrome, then the
// handle and the page data. Kept apart from the database work so the served
// markup can be tested on its own.
func (p *AssetPipeline) ShowcasePage(d ChromeData, data ShowcaseData) ([]byte, error) {
	tmpl, err := p.BuildChromePage("showcase.html", d)
	if err != nil {
		return nil, err
	}
	// json.Marshal HTML-escapes <, >, & by default, so the injected blob cannot
	// break out of the <script> even if a value somehow contained markup.
	blob, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	page := strings.ReplaceAll(string(tmpl), "__SH_HANDLE__", data.Handle)
	page = strings.Replace(page, "/*__SHOWCASE_DATA__*/", string(blob), 1)
	// /webmcp.js is a file on the app origin. This same template is the public
	// person page on the content host and on a person host, where a
	// root-relative script is not that file.
	if d.Base != "" {
		abs := html.EscapeString(strings.TrimRight(d.Base, "/")) + "/webmcp.js?v=1"
		page = strings.Replace(page, `src="/webmcp.js?v=1"`, `src="`+abs+`"`, 1)
	}
	return []byte(page), nil
}

// Emailed one-time links carry their token after "#" (#t=...), so it never
// reaches a server or proxy log: a browser does not send the fragment. The
// link's GET finds no token and answers with fragmentTokenPage, whose script
// reads it and POSTs it back with peek=1; the handler then shows its usual
// confirmation page (peek never acts). Links sent before this change carry
// ?t= and keep working on GET until they expire.

// WriteMessagePage writes the one-message page. A non-empty form (from
// ConfirmForm) takes the place of the way-back link: emailed links open such
// a page on GET and act only when the person presses its button, so a mail
// scanner that follows every link changes nothing.
//
// Exported (cleanup 3.2): a method, not a free function taking *AssetPipeline,
// so it can sit in the hackPages interface moving Hack code (hack_watch.go)
// reaches the pipeline through, since AssetPipeline itself stays unexported
// and in this package. See hack.go's hackPages doc.
func (p *AssetPipeline) WriteMessagePage(w http.ResponseWriter, r *http.Request, base string, status int, message, subtext, backURL, backLabel, form string) {
	tmpl, err := p.BuildChromePage("notfound.html", p.ChromeDataFor(r, base))
	if err != nil {
		// Last-resort inline page so a miss never falls through to nginx's default.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		tail := `<a href="` + html.EscapeString(backURL) + `">` + html.EscapeString(backLabel) + `</a>`
		if form != "" {
			tail = form
		}
		fallback := []byte(`<!doctype html><meta charset=utf-8><meta name=robots content=noindex><title>simple-host</title><h1>` + message + `</h1><p>` + subtext + `</p>` + tail)
		if !p.ChromeProduct {
			page := Themed(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex"><!--sh:theme--><title>simple-host</title></head><body><h1>` + message + `</h1><p>` + subtext + `</p>` + tail + `</body></html>`)
			fallback = httpx.StampNonce(r, p.HostedStatusPage(page))
		}
		_, _ = w.Write(fallback)
		return
	}

	page := string(httpx.StampNonce(r, tmpl))
	if form != "" {
		page = strings.Replace(page, `<a class="btn-primary" href="__SH_BACKLINK_URL__">__SH_BACKLINK_LABEL__</a>`, form, 1)
	}
	page = strings.ReplaceAll(page, "__SH_MESSAGE__", message)
	page = strings.ReplaceAll(page, "__SH_SUBTEXT__", subtext)
	page = strings.ReplaceAll(page, "__SH_BACKLINK_URL__", backURL)
	page = strings.ReplaceAll(page, "__SH_BACKLINK_LABEL__", backLabel)
	if status != http.StatusNotFound {
		// Same layout, without the "404".
		page = strings.Replace(page, `<div class="nf-code">404</div>`, "", 1)
		page = strings.Replace(page, "<title>404 · Not found · simple-host</title>", "<title>simple-host</title>", 1)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(page))
}

var ServiceErrorPage = Themed(`<!doctype html><meta charset=utf-8><meta name=robots content=noindex><!--sh:theme--><title>Temporarily unavailable</title><h1>Temporarily unavailable</h1><p>Please try again in a moment.</p>`)
