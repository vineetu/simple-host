package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
)

// The site chrome — one header, one footer, one stylesheet — is defined once,
// in static/partials/ and static/site.css, and injected into every page at
// serve time. A page opts in by carrying three marker comments:
//
//	<!--sh:head-->    in <head>, before the page's own <style>: site.css + the pre-paint theme
//	<!--sh:header-->  first thing in <body>
//	<!--sh:footer-->  after the page's content, before its scripts
//
// The theme — Match my system, Light or Dark, one choice under localStorage
// 'sh-theme' for every page — is partials/theme.html, which the head partial
// includes. A page with no other chrome (the first-run wizard, the offline and
// take-down pages, the bare sign-in-failed page) carries <!--sh:theme--> in its
// <head> for the same script alone. No page has a theme rule of its own.
//
// Every path that serves an HTML page goes through withChrome: the file server
// (ChromeFileServer, including "/" → index.html), ServeStaticPage, the
// host-Rewritten install.html on other instances (ServeRewrittenAsset), and the
// showcase and 404 templates (renderShowcase, RenderNotFound). A page without
// markers — setup.html, the first-run wizard — passes through untouched.
const (
	markerHead   = "<!--sh:head-->"
	markerHeader = "<!--sh:header-->"
	markerFooter = "<!--sh:footer-->"
	markerTheme  = "<!--sh:theme-->"
	// markerOwner is the owner line on Privacy and Support. It renders only
	// on the hosted services; elsewhere the marker is removed.
	markerOwner = "<!--sh:owner-->"
	// markerSignin loads the shared sign-in card. Only the pages that mount
	// SHSignIn carry it, so the script is not on every chromed page.
	markerSignin = "<!--sh:signin-->"
)

var (
	// html/template strips literal comments, so partials emit the hosted markers
	// through fixed trusted constants before hostedBlocks filters the page.
	ChromeTemplates = template.Must(template.New("").Funcs(template.FuncMap{
		"hostedStart": func() template.HTML { return template.HTML("<!--sh:hosted-->") },
		"hostedEnd":   func() template.HTML { return template.HTML("<!--/sh:hosted-->") },
	}).ParseFS(StaticFiles, "static/partials/*.html"))
	hostInkVersion = staticHash("host-ink.css")
	// Bare gates remain self-contained: handwriting is an embedded data font.
	HostStatusCSS = func() string {
		b, err := StaticFiles.ReadFile("static/host-status.css")
		if err != nil {
			panic(err)
		}
		return string(b)
	}()

	// SiteCSSVersion busts caches on site.css whenever its bytes change.
	SiteCSSVersion = staticHash("site.css")

	// SigninJSVersion is the ?v= hash of the served (message-injected) signin.js bytes.
	SigninJSVersion = sync.OnceValue(func() string { return shortHash(servedSigninJS()) })
)

// staticHash is the cache-busting ?v= value of an embedded static file.
func staticHash(name string) string {
	b, err := StaticFiles.ReadFile("static/" + name)
	if err != nil {
		panic(name + " missing from the embedded static files: " + err.Error())
	}
	return shortHash(b)
}

func shortHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:10]
}

// ChromeData is everything the partials vary on. Every field is drawn from a
// small fixed set, so it is also a safe cache key.
type ChromeData struct {
	ProductStyleVersion string
	ProductStylePath    string
	ProductStyleClass   string
	// Base prefixes every chrome link. Empty on the main origin; the main
	// site's absolute URL when the page is served from the content host or a
	// custom domain, where "/install.html" would resolve to the wrong server.
	Base string
	// Current names the page being served, for aria-current. Empty when the
	// page is not in the nav (a showcase, a 404).
	Current string
	// Product is the product's (Simple Hack's) chrome; false leaves the
	// header and footer as they are.
	Product    bool
	CSSVersion string
	// SigninVersion busts caches on /signin.js, the same way CSSVersion does
	// for site.css. A page carrying <!--sh:signin--> is given
	// <script src="{Base}/signin.js?v={SigninVersion}"></script>.
	SigninVersion string
	// Owner is "Simple Host" on simple-host.app and "Simple Hack" on
	// simple-hack.app, for the line naming who runs the service. Empty on
	// every other instance, which someone else runs under their own name.
	Owner string
	// PrivacyURL is PRIVACY_URL. The footer links it inside an <!--sh:self-->
	// block, so it never reaches a hosted page even if set there.
	PrivacyURL string
}

// HostedOwner names the hosted service this process is, or "".
func (p *AssetPipeline) HostedOwner() string {
	if p.Pres.ProductAssets {
		if p.IsCanonicalInstance() {
			return p.Pres.ProductName
		}
		return ""
	}
	if InstanceIsHostedService {
		return "Simple Host"
	}
	return ""
}

// navKeys maps a request path to the chrome link that names it.
var navKeys = map[string]string{
	"/":                        "home",
	"/install.html":            "install",
	"/dashboard":               "dashboard",
	"/enterprise":              "enterprise",
	"/enterprise.html":         "enterprise",
	"/enterprise/brief":        "enterprise",
	"/enterprise/architecture": "enterprise",
	"/companies":               "companies",
	"/features":                "features",
	"/features.html":           "features",
	"/pricing":                 "pricing",
	"/pricing.html":            "pricing",
	"/architecture.html":       "architecture",
	"/privacy.html":            "privacy",
	"/terms":                   "terms",
	"/support":                 "support",
	"/report":                  "report",
}

// ChromeDataFor derives the chrome for one request. base is "" for pages on
// the main origin.
func (p *AssetPipeline) ChromeDataFor(r *http.Request, base string) ChromeData {
	current := navKeys[r.URL.Path]
	return ChromeData{
		ProductStyleVersion: p.Product.StyleVersion, ProductStylePath: p.Pres.StylePath, ProductStyleClass: p.Pres.StyleClass,
		Base:          base,
		Current:       current,
		Product:       p.ChromeProduct,
		CSSVersion:    SiteCSSVersion,
		SigninVersion: SigninJSVersion(),
		Owner:         p.HostedOwner(),
		PrivacyURL:    p.limits.PrivacyURL,
	}
}

// RenderAppShell is static/hack/app.html (body) with its chrome applied:
// Product=false, since the kit already carries Simple Hack's own look, and
// the sign-in script tag only when signin is true (only /signin mounts
// window.SHSignIn).
func (p *AssetPipeline) RenderAppShell(r *http.Request, body []byte, signin bool) ([]byte, error) {
	if !signin {
		body = bytes.ReplaceAll(body, []byte(markerSignin), nil)
	}
	data := p.ChromeDataFor(r, "")
	data.Product = false
	page, err := withChrome(body, data)
	if err != nil {
		return nil, err
	}
	// app.html does not go through BuildChromePage, so it needs the
	// self-hosted pass here (shell.js reads the contact from a data attribute).
	if p.HostedOwner() == "" {
		page = p.selfHostedChromeRewrite(page)
	}
	return page, nil
}

// WithChromeForTest is withChrome(page, p.ChromeDataFor(r, base)), for the
// product packages' chrome tests.
func (p *AssetPipeline) WithChromeForTest(r *http.Request, base string, page []byte) ([]byte, error) {
	return withChrome(page, p.ChromeDataFor(r, base))
}

// withChrome replaces each marker in page with its rendered partial. Each
// marker is replaced once; tests check a page's source carries each exactly once.
func withChrome(page []byte, d ChromeData) ([]byte, error) {
	if !bytes.Contains(page, []byte("<!--sh:")) {
		return page, nil
	}
	hasHead := bytes.Contains(page, []byte(markerHead))
	for _, m := range []struct{ marker, partial string }{
		{markerHead, "head.html"},
		{markerHeader, "header.html"},
		{markerFooter, "footer.html"},
		{markerTheme, "theme.html"},
		{markerOwner, "owner.html"},
	} {
		if !bytes.Contains(page, []byte(m.marker)) {
			continue
		}
		var buf bytes.Buffer
		if err := ChromeTemplates.ExecuteTemplate(&buf, m.partial, d); err != nil {
			return nil, err
		}
		page = bytes.Replace(page, []byte(m.marker), bytes.TrimRight(buf.Bytes(), "\n"), 1)
	}
	if bytes.Contains(page, []byte(markerSignin)) {
		ver := d.SigninVersion
		if ver == "" {
			ver = SigninJSVersion()
		}
		src := template.HTMLEscapeString(d.Base) + "/signin.js?v=" + template.HTMLEscapeString(ver)
		tag := []byte(`<script src="` + src + `"></script>`)
		page = bytes.Replace(page, []byte(markerSignin), tag, 1)
	}
	if hasHead {
		script, err := StaticFiles.ReadFile("static/arrival.js")
		if err != nil {
			return nil, err
		}
		tag := append([]byte("<script>"), script...)
		tag = append(tag, []byte("</script>")...)
		page = bytes.Replace(page, []byte("</head>"), append(tag, []byte("</head>")...), 1)
	}
	if d.Product {
		page = addProductInk(page, d.Base, d.ProductStyleVersion, d.ProductStylePath, d.ProductStyleClass)
	} else if hasHead {
		page = addHostInk(page, d.Base)
	}
	return hostedBlocks(page, d.Owner != ""), nil
}

// The shared app pages get Host's ink after their layout styles. This path
// never assembles or rewrites a user's deployed HTML.
var remoteFontLinks = regexp.MustCompile(`(?i)<link\b[^>]*href=["']https://fonts\.(?:googleapis|gstatic)\.com[^>]*>`)

func addHostInk(page []byte, base string) []byte {
	if bytes.Contains(page, []byte("/host-ink.css?v=")) {
		return page
	}
	// Older setup/marketing pages used remote fonts. Host now serves its own.
	page = remoteFontLinks.ReplaceAll(page, nil)
	tag := []byte(`<link rel="stylesheet" href="` + template.HTMLEscapeString(base) + `/host-ink.css?v=` + hostInkVersion + `"><script>document.documentElement.classList.add('sh-host');</script>`)
	return bytes.Replace(page, []byte("</head>"), append(tag, []byte("</head>")...), 1)
}

// Add the hosted-only theme after a page's layout CSS. Simple Host never loads it.
func addProductInk(page []byte, base, version, path, class string) []byte {
	if bytes.Contains(page, []byte("/"+path+"?v=")) {
		return page
	}
	tag := []byte(`<link rel="stylesheet" href="` + template.HTMLEscapeString(base) + `/` + template.HTMLEscapeString(path) + `?v=` + version + `"><script>document.documentElement.classList.add('` + template.HTMLEscapeString(class) + `');</script>`)
	if bytes.Contains(page, []byte("</head>")) {
		return bytes.Replace(page, []byte("</head>"), append(tag, []byte("</head>")...), 1)
	}
	return append(tag, page...)
}

// Go-built status pages were themed at package init, before hosted mode is known.
func (p *AssetPipeline) HostedStatusPage(page string) []byte {
	if p.ChromeProduct {
		d := ChromeData{Product: true, ProductStyleVersion: p.Product.StyleVersion, ProductStylePath: p.Pres.StylePath, ProductStyleClass: p.Pres.StyleClass, Base: p.InkBaseURL(), CSSVersion: SiteCSSVersion, SigninVersion: SigninJSVersion()}
		if !strings.Contains(page, "<head>") && !strings.Contains(page, "<head ") {
			page = "<!doctype html><html><head><meta charset=utf-8><meta name=viewport content=\"width=device-width, initial-scale=1\">" + markerHead + "</head><body>" + page + "</body></html>"
		} else {
			if !strings.Contains(page, `name="viewport"`) {
				page = strings.Replace(page, "</head>", `<meta name="viewport" content="width=device-width, initial-scale=1"></head>`, 1)
			}
			page = strings.Replace(page, "</head>", markerHead+"</head>", 1)
		}
		page = strings.Replace(page, "<body>", `<body class="sh-status">`+markerHeader+`<main>`, 1)
		page = strings.Replace(page, "</body>", "</main>"+markerFooter+"</body>", 1)
		out, err := withChrome([]byte(page), d)
		if err == nil {
			return p.ProductInstanceText(out)
		}
		return addProductInk([]byte(page), p.InkBaseURL(), p.Product.StyleVersion, p.Pres.StylePath, p.Pres.StyleClass)
	}
	if strings.Contains(page, "</head>") && !strings.Contains(page, `name="viewport"`) {
		page = strings.Replace(page, "</head>", `<meta name="viewport" content="width=device-width,initial-scale=1"></head>`, 1)
	}
	style := `<style>` + HostStatusCSS + `</style>`
	if strings.Contains(page, "</head>") {
		page = strings.Replace(page, "</head>", style+"</head>", 1)
	} else {
		page = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` + style + `</head><body>` + page + `</body></html>`
	}
	return []byte(page)
}

// Themed fills the theme marker of a page built in Go that has no other
// chrome. It runs once, at package init.
func Themed(page string) string {
	b, err := withChrome([]byte(page), ChromeData{})
	if err != nil {
		panic("theme partial: " + err.Error())
	}
	return string(b)
}

type chromeCacheKey struct {
	name string
	d    ChromeData
}

// ChromeCache holds assembled static pages. Bounded: a handful of pages times
// a handful of ChromeData values.
var ChromeCache sync.Map

// BuildChromePage returns the embedded static/<name> with its chrome filled in and
// this install's limits in its copy (limitstext.go). The enterprise-* pages
// describe the other product, whose limits are its own, so they keep theirs.
func (p *AssetPipeline) BuildChromePage(name string, d ChromeData) ([]byte, error) {
	page, err := p.chromePageRaw(name, d)
	if err == nil && name == "enterprise.html" {
		if hook := p.Product.Hooks.PersonalPresentationHTML; hook != nil {
			page = hook(name, page)
		}
	}
	if err != nil || strings.HasPrefix(name, "enterprise-") || name == "enterprise.html" {
		return page, err
	}
	return p.PresentChromePage(name, page), nil
}

func (p *AssetPipeline) PresentChromePage(name string, page []byte) []byte {
	if p.Pres.ProductAssets {
		if hook := p.Product.Hooks.PersonalPresentationHTML; hook != nil {
			page = hook(name, page)
		}
	}
	page = hostedBlocks(p.ProductInstanceText(p.limitsText.apply(page)), p.HostedOwner() != "")
	page = hasProductBlocks(page, p.HostedOwner() != "" || p.Pres.ProductAssets)
	if p.HostedOwner() == "" {
		page = p.selfHostedChromeRewrite(page)
	}
	return page
}

// selfHostedChromeRewrite is the self-hosted pass every chromed page gets.
// Callers gate it on HostedOwner() == "", not on InstanceHosts (which is
// non-nil on simple-hack.app too), so the hosted services' bytes stay
// untouched. hostedBlocks has already dropped the hosted-only blocks; what
// still names simple-host.app is a link InstanceHosts repoints, plus the
// support address, which only SUPPORT_EMAIL (or IDLE_REPLY_TO) can replace.
func (p *AssetPipeline) selfHostedChromeRewrite(page []byte) []byte {
	// Must run before InstanceHosts.apply: that pass's blanket
	// "simple-host.app" -> siteDomain replacement would otherwise mangle
	// "support@simple-host.app" into "support@<this domain>" first, a
	// nonexistent address, leaving nothing left for this one to match.
	page = rewriteSupportAddress(page)
	return InstanceHosts.Apply(page)
}

// supportMailtoLinkRe matches the whole <a href="mailto:support@simple-host.app...">...</a>
// element (every page that links the hosted support address uses this exact
// shape: no query string, visible text identical to the address).
var supportMailtoLinkRe = regexp.MustCompile(`<a href="mailto:support@simple-host\.app(?:\?[^"]*)?">support@simple-host\.app</a>`)

// codeScriptRe matches one <script ...>...</script> element. Simple Host's own
// markup never nests or malforms a script tag, so a non-greedy match is exact.
var codeScriptRe = regexp.MustCompile(`(?is)<script([^>]*)>.*?</script>`)
var jsonScriptTypeRe = regexp.MustCompile(`(?i)type\s*=\s*["']?application/(?:ld\+json|json)["']?`)

// rewriteOutsideScript applies htmlFn to every part of page outside a
// JavaScript <script> element, and jsonFn to the content of a JSON data
// island (application/ld+json or application/json), which needs JSON-safe,
// not HTML-escaped, substitution. IDLE_REPLY_TO is not validated as strictly
// as SUPPORT_EMAIL, so this is the structural guard that the contact never
// lands inside code; a script that needs it reads a data attribute instead.
func rewriteOutsideScript(page []byte, htmlFn, jsonFn func([]byte) []byte) []byte {
	var out []byte
	last := 0
	for _, loc := range codeScriptRe.FindAllSubmatchIndex(page, -1) {
		start, end, attrStart, attrEnd := loc[0], loc[1], loc[2], loc[3]
		out = append(out, htmlFn(page[last:start])...)
		if jsonScriptTypeRe.Match(page[attrStart:attrEnd]) {
			out = append(out, jsonFn(page[start:end])...)
		} else {
			out = append(out, page[start:end]...)
		}
		last = end
	}
	return append(out, htmlFn(page[last:])...)
}

// jsonStringBody is s as it would appear inside a JSON string's quotes.
func jsonStringBody(s string) string {
	b, err := json.Marshal(s)
	if err != nil || len(b) < 2 {
		return s
	}
	return string(b[1 : len(b)-1])
}

// rewriteSupportAddress swaps the hosted service's support address for this
// install's own contact. With no configured address (auth.SupportContact has
// no "@"), the mailto anchor and plain mentions become a phrase instead, so a
// self-hosted install never shows the hosted inbox as its own.
//
// Every replacement is literal (ReplaceAllLiteral, so a $ in the contact never
// expands), HTML-escaped in page text, JSON-escaped in data islands, and
// skipped inside executable scripts: an apostrophe in SUPPORT_EMAIL once broke
// /report's inline script.
func rewriteSupportAddress(page []byte) []byte {
	if strings.Contains(auth.SupportContact, "@") {
		escAddr := []byte(html.EscapeString(auth.SupportContact))
		jsonAddr := []byte(jsonStringBody(auth.SupportContact))
		link := []byte(`<a href="mailto:` + string(escAddr) + `">` + string(escAddr) + `</a>`)
		return rewriteOutsideScript(page, func(seg []byte) []byte {
			seg = supportMailtoLinkRe.ReplaceAllLiteral(seg, link)
			return bytes.ReplaceAll(seg, []byte("support@simple-host.app"), escAddr)
		}, func(seg []byte) []byte {
			return bytes.ReplaceAll(seg, []byte("support@simple-host.app"), jsonAddr)
		})
	}
	phrase := []byte("the person who runs this server")
	return rewriteOutsideScript(page, func(seg []byte) []byte {
		seg = supportMailtoLinkRe.ReplaceAllLiteral(seg, phrase)
		return bytes.ReplaceAll(seg, []byte("support@simple-host.app"), phrase)
	}, func(seg []byte) []byte {
		return bytes.ReplaceAll(seg, []byte("support@simple-host.app"), phrase)
	})
}

// hostedBlocks keeps the text that is true for this server. A page marks
// what only simple-host.app or simple-hack.app can say (Claude's directory,
// the published plugins) with <!--sh:hosted-->...<!--/sh:hosted-->, and what
// another server says instead with <!--sh:self-->...<!--/sh:self-->.
func hostedBlocks(page []byte, hosted bool) []byte {
	keepName, dropName := "hosted", "self"
	if !hosted {
		keepName, dropName = dropName, keepName
	}
	return stripMarkedBlocks(page, keepName, dropName)
}

// hasProductBlocks keeps <!--sh:hashack-->...<!--/sh:hashack--> text only
// where Simple Hack exists (the hosted services and any product-edition
// instance) and <!--sh:nohack--> wording on a self-hosted Host edition, which
// runs no events.
func hasProductBlocks(page []byte, hasProduct bool) []byte {
	keepName, dropName := "hashack", "nohack"
	if !hasProduct {
		keepName, dropName = dropName, keepName
	}
	return stripMarkedBlocks(page, keepName, dropName)
}

// stripMarkedBlocks removes every <!--sh:dropName-->...<!--/sh:dropName-->
// span with its content and unwraps every keepName pair.
func stripMarkedBlocks(page []byte, keepName, dropName string) []byte {
	if !bytes.Contains(page, []byte("<!--sh:")) {
		return page
	}
	return stripSpans(page, "<!--sh:%s-->", "<!--/sh:%s-->", keepName, dropName)
}

// stripBracketMarkers is stripMarkedBlocks for [[name]]...[[/name]] markers,
// in the one asset (features.json) whose markers never become HTML comments.
func stripBracketMarkers(body []byte, keepName, dropName string) []byte {
	return stripSpans(body, "[[%s]]", "[[/%s]]", keepName, dropName)
}

// stripSpans drops each open/close span named dropName with its content and
// removes the markers named keepName; openFmt and closeFmt are fmt patterns.
func stripSpans(page []byte, openFmt, closeFmt, keepName, dropName string) []byte {
	dropOpen, dropEnd := []byte(fmt.Sprintf(openFmt, dropName)), []byte(fmt.Sprintf(closeFmt, dropName))
	for {
		i := bytes.Index(page, dropOpen)
		if i < 0 {
			break
		}
		j := bytes.Index(page[i:], dropEnd)
		if j < 0 {
			break
		}
		page = append(page[:i:i], page[i+j+len(dropEnd):]...)
	}
	page = bytes.ReplaceAll(page, []byte(fmt.Sprintf(openFmt, keepName)), nil)
	return bytes.ReplaceAll(page, []byte(fmt.Sprintf(closeFmt, keepName)), nil)
}

// The cache key carries no product identity: it assumes one product's files
// per process (ChromeData.Product and ProductStyleVersion separate the editions).
func (p *AssetPipeline) chromePageRaw(name string, d ChromeData) ([]byte, error) {
	key := chromeCacheKey{name, d}
	if v, ok := ChromeCache.Load(key); ok {
		return v.([]byte), nil
	}
	raw, err := p.TextFS().ReadFile("static/" + name)
	if err != nil {
		return nil, err
	}
	if name == "hack-get-started.html" {
		if hook := p.Product.Hooks.ExpandConnectSteps; hook != nil {
			raw = hook(raw)
		}
	}
	page, err := withChrome(raw, d)
	if err != nil {
		return nil, err
	}
	if name == "setup.html" && !d.Product {
		page = addHostInk(page, d.Base)
	}
	ChromeCache.Store(key, page)
	return page, nil
}

// ChromeFileServer puts the chrome on HTML pages the file server would
// otherwise hand out raw, and passes everything else — assets, 404s, the
// /index.html → / redirect — to next. fsys is the file server's own view, so
// a page hidden from it stays hidden here.
func (p *AssetPipeline) ChromeFileServer(fsys fs.FS, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		switch {
		case name == "":
			name = "index.html"
		case name == "index.html" || !strings.HasSuffix(name, ".html") || !fs.ValidPath(name):
			next.ServeHTTP(w, r)
			return
		}
		f, err := fsys.Open(name)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		f.Close()
		body, err := p.BuildChromePage(name, p.ChromeDataFor(r, ""))
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if p.ChromeProduct {
			w.Header().Set("Cache-Control", "no-store")
		}
		// A zero modtime, as the embedded FS reports, so no Last-Modified —
		// exactly what the file server sent for these pages before.
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(httpx.StampNonce(r, body)))
	})
}

// SupportText puts this install's contact (auth.SupportContact) where s
// names the hosted service's support address.
func SupportText(s string) string { return auth.SupportText(s) }
