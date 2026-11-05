package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
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
// (chromeFileServer, including "/" → index.html), serveStaticPage, the
// host-rewritten install.html on other instances (serveRewrittenAsset), and the
// showcase and 404 templates (renderShowcase, renderNotFound). A page without
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
	}).ParseFS(staticFiles, "static/partials/*.html"))
	hackInkVersion = func() string {
		b, err := staticFiles.ReadFile("static/hack-ink.css")
		if err != nil {
			panic(err)
		}
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])[:10]
	}()
	hostInkVersion = func() string {
		b, err := staticFiles.ReadFile("static/host-ink.css")
		if err != nil {
			panic(err)
		}
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])[:10]
	}()
	// Bare gates remain self-contained: handwriting is an embedded data font.
	hostStatusCSS = func() string {
		b, err := staticFiles.ReadFile("static/host-status.css")
		if err != nil {
			panic(err)
		}
		return string(b)
	}()

	// siteCSSVersion busts caches on site.css whenever its bytes change.
	siteCSSVersion = func() string {
		b, err := staticFiles.ReadFile("static/site.css")
		if err != nil {
			panic("site.css missing from the embedded static files: " + err.Error())
		}
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])[:10]
	}()
)

var (
	signinVerOnce sync.Once
	signinVer     string
)

// SigninJSVersion is the ?v= hash of the served (message-injected) signin.js bytes.
func SigninJSVersion() string {
	signinVerOnce.Do(func() {
		sum := sha256.Sum256(servedSigninJS())
		signinVer = hex.EncodeToString(sum[:])[:10]
	})
	return signinVer
}

// ChromeData is everything the partials vary on. Every field is drawn from a
// small fixed set, so it is also a safe cache key.
type ChromeData struct {
	// Base prefixes every chrome link. Empty on the main origin; the main
	// site's absolute URL when the page is served from the content host or a
	// custom domain, where "/install.html" would resolve to the wrong server.
	Base string
	// Current names the page being served, for aria-current. Empty when the
	// page is not in the nav (a showcase, a 404).
	Current string
	// HackHome is the hackathons page served as simple-hack.app's homepage.
	// There "Home" would be a link back to this same page and "For hackathons"
	// a second one, so Home points at the main product and the duplicate goes.
	HackHome bool
	// Hack is the simple-hack.app chrome (the pipeline's chromeHack, fixed at
	// construction from the edition's Presentation().HackAssets); false
	// leaves the header and footer as they are.
	Hack       bool
	CSSVersion string
	// SigninVersion busts caches on /signin.js, the same way CSSVersion does
	// for site.css. A page carrying <!--sh:signin--> is given
	// <script src="{Base}/signin.js?v={SigninVersion}"></script>.
	SigninVersion string
	// Owner is "Simple Host" on simple-host.app and "Simple Hack" on
	// simple-hack.app, for the line naming who runs the service. Empty on
	// every other instance, which someone else runs under their own name.
	Owner string
	// PrivacyURL is PRIVACY_URL, this install's own privacy policy. The
	// footer partial links to it, on a self-hosted instance only ({{if
	// .PrivacyURL}}, inside an <!--sh:self--> block so it never reaches a
	// hosted page's rendered bytes even if somehow set there).
	PrivacyURL string
}

// hostedOwner names the hosted service this process is, or "".
func (p *assetPipeline) hostedOwner() string {
	if p.chromeHack {
		if p.inkBaseURL() == canonicalHackBaseURL {
			return "Simple Hack"
		}
		return ""
	}
	if !p.pres.HackAssets && instanceIsHostedService {
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
	"/hackathons":              "hackathons",
	"/hackathons.html":         "hackathons",
	"/companies":               "companies",
	"/features":                "features",
	"/features.html":           "features",
	"/pricing":                 "pricing",
	"/pricing.html":            "pricing",
	"/docs.html":               "docs",
	"/architecture.html":       "architecture",
	"/privacy.html":            "privacy",
	"/terms":                   "terms",
	"/support":                 "support",
	"/report":                  "report",
}

// chromeDataFor derives the chrome for one request. base is "" for pages on
// the main origin.
func (p *assetPipeline) chromeDataFor(r *http.Request, base string) ChromeData {
	current := navKeys[r.URL.Path]
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(host)
	return ChromeData{
		Base:          base,
		Current:       current,
		HackHome:      current == "hackathons" && strings.HasPrefix(host, "simple-hack."),
		Hack:          p.chromeHack,
		CSSVersion:    siteCSSVersion,
		SigninVersion: SigninJSVersion(),
		Owner:         p.hostedOwner(),
		PrivacyURL:    p.limits.PrivacyURL,
	}
}

// RenderAppShell is static/hack/app.html (body) with its chrome applied: the
// kit's own presentation (ChromeData.Hack=false, since the kit already
// carries Simple Hack's own look), and the sign-in script tag only when
// signin is true (only /signin mounts window.SHSignIn; every other route
// strips the marker instead of loading it).
//
// Exported for hackPages (cleanup 3.2 slice 2a): RegisterHackUI
// (hack_ui.go), moving to internal/hackhttp, used to do this inline with
// withChrome/ChromeData directly, both unexported handler types/functions.
func (p *assetPipeline) RenderAppShell(r *http.Request, body []byte, signin bool) ([]byte, error) {
	if !signin {
		body = bytes.ReplaceAll(body, []byte(markerSignin), nil)
	}
	data := p.chromeDataFor(r, "")
	data.Hack = false
	return withChrome(body, data)
}

// withChrome replaces each marker in page with its rendered partial. Each
// marker is replaced once; a page's source is checked by tests to carry each
// exactly once.
// WithChromeForTest applies p's chrome to page the way withChrome(page,
// p.chromeDataFor(r, base)) does. Test hook (cleanup 3.2 slice 3): the
// moved hack_eventpage_test.go (TestHackChromeHeader) checks raw
// chrome-marker substitution directly; ChromeData and withChrome are both
// unexported, so a cross-package caller cannot build the call itself.
func (p *assetPipeline) WithChromeForTest(r *http.Request, base string, page []byte) ([]byte, error) {
	return withChrome(page, p.chromeDataFor(r, base))
}

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
		script, err := staticFiles.ReadFile("static/arrival.js")
		if err != nil {
			return nil, err
		}
		tag := append([]byte("<script>"), script...)
		tag = append(tag, []byte("</script>")...)
		page = bytes.Replace(page, []byte("</head>"), append(tag, []byte("</head>")...), 1)
	}
	if d.Hack {
		page = addHackInk(page, d.Base)
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
func addHackInk(page []byte, base string) []byte {
	if bytes.Contains(page, []byte("/hack-ink.css?v=")) {
		return page
	}
	tag := []byte(`<link rel="stylesheet" href="` + template.HTMLEscapeString(base) + `/hack-ink.css?v=` + hackInkVersion + `"><script>document.documentElement.classList.add('sh-hack');</script>`)
	if bytes.Contains(page, []byte("</head>")) {
		return bytes.Replace(page, []byte("</head>"), append(tag, []byte("</head>")...), 1)
	}
	return append(tag, page...)
}

// Go-built status pages were themed at package init, before hosted mode is known.
func (p *assetPipeline) hostedStatusPage(page string) []byte {
	if p.chromeHack {
		d := ChromeData{Hack: true, Base: p.inkBaseURL(), CSSVersion: siteCSSVersion, SigninVersion: SigninJSVersion()}
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
			return p.HackInstanceText(out)
		}
		return addHackInk([]byte(page), p.inkBaseURL())
	}
	if strings.Contains(page, "</head>") && !strings.Contains(page, `name="viewport"`) {
		page = strings.Replace(page, "</head>", `<meta name="viewport" content="width=device-width,initial-scale=1"></head>`, 1)
	}
	style := `<style>` + hostStatusCSS + `</style>`
	if strings.Contains(page, "</head>") {
		page = strings.Replace(page, "</head>", style+"</head>", 1)
	} else {
		page = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` + style + `</head><body>` + page + `</body></html>`
	}
	return []byte(page)
}

// themed fills the theme marker of a page built in Go that has no other
// chrome. It runs once, at package init.
func themed(page string) string {
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

// chromeCache holds assembled static pages. Bounded: a handful of pages times
// a handful of ChromeData values.
var chromeCache sync.Map

// chromePage returns the embedded static/<name> with its chrome filled in and
// this install's limits in its copy (limitstext.go). The enterprise-* pages
// describe the other product, whose limits are its own, so they keep theirs.
func (p *assetPipeline) chromePage(name string, d ChromeData) ([]byte, error) {
	page, err := chromePageRaw(name, d)
	if err != nil || strings.HasPrefix(name, "enterprise-") || name == "enterprise.html" {
		return page, err
	}
	if p.pres.HackAssets {
		page = hackPersonalPresentationHTML(name, page)
	}
	page = hostedBlocks(p.HackInstanceText(p.limitsText.apply(page)), p.hostedOwner() != "")
	if p.hostedOwner() == "" {
		page = p.selfHostedChromeRewrite(page)
	}
	return page, nil
}

// selfHostedChromeRewrite is the one centralized self-hosted pass every
// chromed page gets, not just the five enumerated rewrittenAssets
// (instancehost.go). Runs only when hostedOwner() == "" -- a genuinely
// self-hosted instance, never simple-host.app or simple-hack.app themselves
// (instanceHosts can be non-nil there too, since a Hack instance's own
// SITE_DOMAIN differs from the Host-specific canonicalSiteDomain constant;
// hostedOwner() is what actually tells the two apart, so gating on it here
// is what keeps the hosted edition's served bytes untouched).
//
// By the time this runs, hostedBlocks has already dropped every
// <!--sh:hosted-->-wrapped block: the plugins, the connector directory, our
// own self-host marketing. What is left and still names
// simple-host.app/simple-hack.app is a link to an equivalent page on this
// install (a dashboard, a skill download, a docs command, an og:url) --
// exactly what instanceHosts already knows how to repoint, the same
// substitution the five rewrittenAssets have always gotten (ui.go,
// instancehost.go). The one address instanceHosts cannot safely guess is
// the operator's own support contact: SUPPORT_EMAIL (or IDLE_REPLY_TO,
// supportText's older mechanism) replaces support@simple-host.app in text
// and mailto: links; left unset, an operator sees the plain string removed
// nowhere -- call sites that want "no address at all" check
// auth.SupportContact themselves (account_data.go's supportText).
func (p *assetPipeline) selfHostedChromeRewrite(page []byte) []byte {
	// Must run before instanceHosts.apply: that pass's blanket
	// "simple-host.app" -> siteDomain replacement would otherwise mangle
	// "support@simple-host.app" into "support@<this domain>" first, a
	// nonexistent address, leaving nothing left for this one to match.
	page = rewriteSupportAddress(page)
	return instanceHosts.apply(page)
}

// supportMailtoLinkRe matches the whole <a href="mailto:support@simple-host.app...">...</a>
// element (every page that links the hosted support address uses this exact
// shape: no query string, visible text identical to the address).
var supportMailtoLinkRe = regexp.MustCompile(`<a href="mailto:support@simple-host\.app(?:\?[^"]*)?">support@simple-host\.app</a>`)

// rewriteSupportAddress swaps the hosted service's support address for this
// install's own contact. When an operator set SUPPORT_EMAIL or
// IDLE_REPLY_TO, auth.SupportContact is a real address and the mailto link
// keeps working, now pointed at it (existing behaviour). Left unset,
// SupportContact is just a phrase ("whoever runs this server") with no "@",
// so there is no address to link to: the whole anchor is replaced with
// plain text, and the plain-text mentions (features.html, docs.html,
// report.html's JS strings) with the same phrase, so a self-hosted install
// never shows Vineet's inbox as if it were its own.
func rewriteSupportAddress(page []byte) []byte {
	if strings.Contains(auth.SupportContact, "@") {
		addr := []byte(auth.SupportContact)
		page = supportMailtoLinkRe.ReplaceAll(page, []byte(`<a href="mailto:`+auth.SupportContact+`">`+auth.SupportContact+`</a>`))
		return bytes.ReplaceAll(page, []byte("support@simple-host.app"), addr)
	}
	phrase := []byte("the person who runs this server")
	page = supportMailtoLinkRe.ReplaceAll(page, phrase)
	return bytes.ReplaceAll(page, []byte("support@simple-host.app"), phrase)
}

// hostedBlocks keeps the text that is true for this server. A page marks
// what only simple-host.app or simple-hack.app can say (Claude's directory,
// the published plugins) with <!--sh:hosted-->...<!--/sh:hosted-->, and what
// another server says instead with <!--sh:self-->...<!--/sh:self-->.
func hostedBlocks(page []byte, hosted bool) []byte {
	if !bytes.Contains(page, []byte("<!--sh:")) {
		return page
	}
	keep, drop := "hosted", "self"
	if !hosted {
		keep, drop = drop, keep
	}
	open, end := []byte("<!--sh:"+drop+"-->"), []byte("<!--/sh:"+drop+"-->")
	for {
		i := bytes.Index(page, open)
		if i < 0 {
			break
		}
		j := bytes.Index(page[i:], end)
		if j < 0 {
			break
		}
		page = append(page[:i:i], page[i+j+len(end):]...)
	}
	page = bytes.ReplaceAll(page, []byte("<!--sh:"+keep+"-->"), nil)
	return bytes.ReplaceAll(page, []byte("<!--/sh:"+keep+"-->"), nil)
}

func chromePageRaw(name string, d ChromeData) ([]byte, error) {
	key := chromeCacheKey{name, d}
	if v, ok := chromeCache.Load(key); ok {
		return v.([]byte), nil
	}
	raw, err := staticFiles.ReadFile("static/" + name)
	if err != nil {
		return nil, err
	}
	if name == "hack-get-started.html" {
		raw = expandConnectSteps(raw)
	}
	page, err := withChrome(raw, d)
	if err != nil {
		return nil, err
	}
	if name == "setup.html" && !d.Hack {
		page = addHostInk(page, d.Base)
	}
	if name == "privacy.html" {
		// Stripped once here; SetImageScanOn drops these cached copies.
		page = stripImageScanItem(page)
	}
	chromeCache.Store(key, page)
	return page, nil
}

// chromeFileServer puts the chrome on HTML pages the file server would
// otherwise hand out raw, and passes everything else — assets, 404s, the
// /index.html → / redirect — to next. fsys is the file server's own view, so
// a page hidden from it stays hidden here.
func (p *assetPipeline) chromeFileServer(fsys fs.FS, next http.Handler) http.Handler {
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
		body, err := p.chromePage(name, p.chromeDataFor(r, ""))
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if p.chromeHack {
			w.Header().Set("Cache-Control", "no-store")
		}
		// A zero modtime, as the embedded FS reports, so no Last-Modified —
		// exactly what the file server sent for these pages before.
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(stampNonce(r, body)))
	})
}

// chromeBase is the link base for the showcase and 404 pages, which are also
// served off the content host (nginx proxies those as /internal/…) and custom
// domains, where a relative link would land on the wrong server.
func (h *SiteHandler) chromeBase(r *http.Request) string {
	// Pages rendered for the content host (/internal/...) or on a person host
	// link back to the main site absolutely.
	if strings.HasPrefix(r.URL.Path, "/internal/") || h.isPlatformSubdomainHost(requestHostName(r)) || h.isSiteHostName(r.Context(), requestHostName(r)) {
		return h.mainSiteURL()
	}
	return ""
}
