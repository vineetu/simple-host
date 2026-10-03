package handler

import (
	"bytes"
	"net/http"
	"path"
	"strings"
	"time"
)

// canonicalSiteDomain is the hostname baked into every served text asset: the
// skills, llms.txt, install.html, auth.js and the OpenAPI spec all name it
// literally. That is correct for simple-host.app and wrong everywhere else.
//
// An instance running on another domain has to describe itself, or an agent
// following its documentation publishes to simple-host.app instead. That is not
// a cosmetic bug: a hackathon's entries would land on somebody else's server.
const canonicalSiteDomain = "simple-host.app"

// rewrittenAssets are served through a handler that substitutes the instance's
// own hostnames, and hidden from the file server so there is exactly one path
// to each. Binary and vendored assets are excluded: nothing in swagger-ui or an
// image mentions the host, and rewriting bytes we do not own is a bad habit.
var rewrittenAssets = []string{
	"llms.txt",
	"install.html",
	"auth.js",
	"openapi.yaml",
	"openapi.json",
}

// hostRewriter substitutes this instance's hostnames for the canonical ones.
//
// Nil means "no rewriting needed", which is the case on simple-host.app itself,
// where every replacement would be identity. Callers must handle nil rather
// than paying for a no-op pass over every asset in production.
type hostRewriter struct{ r *strings.Replacer }

// newHostRewriter returns nil when this instance IS the canonical one.
//
// Order matters and is the whole subtlety here: "sites.simple-host.app"
// contains "simple-host.app", so the longer hostnames must be listed first or
// the content host would be rewritten into "sites.<newdomain>" with the wrong
// prefix. strings.Replacer prefers the earliest-listed match at a position.
func newHostRewriter(siteDomain, contentHost, cnameTarget string) *hostRewriter {
	if siteDomain == "" || siteDomain == canonicalSiteDomain {
		return nil
	}
	if contentHost == "" {
		contentHost = "sites." + siteDomain
	}
	if cnameTarget == "" {
		cnameTarget = "cname." + siteDomain
	}
	return &hostRewriter{r: strings.NewReplacer(
		// The hosted instance hands out per-person addresses; an instance on
		// another domain keeps the path model (PERSON_HOSTS defaults to off),
		// so its docs describe the path address instead.
		// Per-site addresses (<site>.<handle>.simple-host.app, SITE_HOSTS)
		// become the path address too.
		"<site>.<handle>."+canonicalSiteDomain+"/", contentHost+"/<handle>/<site>/",
		"<site>.<handle>."+canonicalSiteDomain, contentHost+"/<handle>/<site>",
		"<sitename>.<handle>."+canonicalSiteDomain+"/", contentHost+"/<handle>/<sitename>/",
		"<sitename>.<handle>."+canonicalSiteDomain, contentHost+"/<handle>/<sitename>",
		"<name>.<handle>."+canonicalSiteDomain+"/", contentHost+"/<handle>/<name>/",
		"<name>.<handle>."+canonicalSiteDomain, contentHost+"/<handle>/<name>",
		"&lt;site&gt;.&lt;handle&gt;."+canonicalSiteDomain+"/", contentHost+"/&lt;handle&gt;/&lt;site&gt;/",
		"&lt;site&gt;.&lt;handle&gt;."+canonicalSiteDomain, contentHost+"/&lt;handle&gt;/&lt;site&gt;",
		"<handle>."+canonicalSiteDomain+"/v1", contentHost+"/v1",
		"<handle>."+canonicalSiteDomain+"/<sitename>", contentHost+"/<handle>/<sitename>",
		"<handle>."+canonicalSiteDomain+"/<site>", contentHost+"/<handle>/<site>",
		"<handle>."+canonicalSiteDomain+"/<name>", contentHost+"/<handle>/<name>",
		"<handle>."+canonicalSiteDomain+"/", contentHost+"/<handle>/",
		"<handle>."+canonicalSiteDomain, contentHost+"/<handle>",
		"&lt;handle&gt;."+canonicalSiteDomain+"/&lt;site&gt;", contentHost+"/&lt;handle&gt;/&lt;site&gt;",
		"&lt;handle&gt;."+canonicalSiteDomain+"/", contentHost+"/&lt;handle&gt;/",
		"&lt;handle&gt;."+canonicalSiteDomain, contentHost+"/&lt;handle&gt;",
		// Every site shares the content host's origin in the path model.
		"(its own browser origin)", "(one browser origin, shared by every site on this server)",
		"sites."+canonicalSiteDomain, contentHost,
		"cname."+canonicalSiteDomain, cnameTarget,
		canonicalSiteDomain, siteDomain,
	)}
}

// apply rewrites a copy of b. Nil receiver returns b untouched so callers can
// stay branch-free.
func (h *hostRewriter) apply(b []byte) []byte {
	if h == nil {
		return b
	}
	return []byte(h.r.Replace(string(b)))
}

var assetContentTypes = map[string]string{
	".txt":  "text/plain; charset=utf-8",
	".html": "text/html; charset=utf-8",
	".js":   "application/javascript; charset=utf-8",
	".json": "application/json; charset=utf-8",
	".yaml": "application/yaml; charset=utf-8",
}

// serveRewrittenAsset serves one embedded asset with the instance's own
// hostnames (rw, which may be nil) and limits substituted in.
func serveRewrittenAsset(name string, rw *hostRewriter, modTime time.Time) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		var err error
		if strings.HasSuffix(name, ".html") {
			// A page gets the shared chrome first, so the rewrite covers it
			// too; chromePage has already put this install's limits in.
			body, err = chromePage(name, chromeDataFor(r, ""))
			if err == nil {
				body = rw.apply(body)
			}
		} else {
			sourceName := name
			if hackMode {
				switch name {
				case "llms.txt":
					sourceName = "hack-llms.txt"
				case "openapi.yaml", "openapi.json":
					sourceName = "openapi.json"
				}
			}
			body, err = staticFiles.ReadFile("static/" + sourceName)
			if err == nil && hackMode && (name == "openapi.yaml" || name == "openapi.json") {
				body, err = hackOpenAPISpec(body)
			}
			if err == nil {
				body = instanceLimits.apply(rw.apply(body))
			}
			if err == nil && name == "llms.txt" && instanceNote != "" {
				body = append([]byte(instanceNote), body...)
			}
		}
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if ct := assetContentTypes[strings.ToLower(path.Ext(name))]; ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		http.ServeContent(w, r, name, modTime, bytes.NewReader(body))
	})
}

// instanceHosts rewrites the canonical hostnames baked into served assets to
// this instance's own. Nil on simple-host.app, where every substitution is
// identity.
//
// It is a package var because the skills zip builders are package-level
// singletons with sync.Once caches: whichever request builds a zip first
// freezes its contents for the process lifetime. So this must be set before the
// server accepts a request, and SetInstanceHosts is called from main directly
// after config load rather than as a side effect of registering routes.
var instanceHosts *hostRewriter

// SetInstanceHosts configures host rewriting for served assets. Call once, at
// startup, before serving. handoutBase (optional; default siteDomain) is the
// domain people's addresses are handed out under (sitebase.go): it decides
// how the embedded text names them (basetext.go).
func SetInstanceHosts(siteDomain, contentHost, cnameTarget string, handoutBase ...string) {
	base := siteDomain
	if len(handoutBase) > 0 && handoutBase[0] != "" {
		base = handoutBase[0]
	}
	setBaseText(siteDomain, base)
	instanceHosts = newHostRewriter(siteDomain, contentHost, cnameTarget)
	instanceIsCanonical = siteDomain == "" || siteDomain == canonicalSiteDomain
}

// instanceIsCanonical: this is the hosted service (or has no domain), which
// the shared text describes, so llms.txt needs no THIS SERVER note.
var instanceIsCanonical = true

// InstanceFacts is what this install's llms.txt says about itself, above the
// text every install shares (which describes simple-host.app).
type InstanceFacts struct {
	SiteDomain, ContentHost string
	// BaseDomain is the domain people's addresses are handed out under
	// (sitebase.go); "" means SiteDomain.
	BaseDomain string
	// SharedOrigin: every site is served on the content host's one origin
	// (PERSON_HOSTS and SITE_HOSTS off, as on a small box).
	SharedOrigin bool
	// SignInEmail and SignInProviders: how visitors can sign in.
	SignInEmail     bool
	SignInProviders []string
	// Contact: who to ask for help (auth.SupportContact).
	Contact string
}

// instanceNote is prepended to llms.txt on an install other than
// simple-host.app (SetInstanceNote); "" there.
var instanceNote string

// SetInstanceNote writes the THIS SERVER block of llms.txt from f. Call once
// at startup, after SetInstanceHosts; it does nothing on simple-host.app.
func SetInstanceNote(f InstanceFacts) {
	if instanceHosts == nil || instanceIsCanonical {
		instanceNote = ""
		return
	}
	base := f.BaseDomain
	if base == "" {
		base = f.SiteDomain
	}
	var b strings.Builder
	b.WriteString("THIS SERVER (" + f.SiteDomain + ") — read this first: the text after it describes the hosted service at simple-host.app, and where it differs, what is written here is what holds on this server.\n")
	if f.SharedOrigin {
		b.WriteString("- Addresses: every site is at https://" + f.ContentHost + "/<handle>/<site>/, and every site on this server shares that one browser origin: localStorage, sessionStorage, IndexedDB and cookies are shared with every other site here. Keep nothing private in the browser, and prefix browser storage keys with the site's name. A free <name>." + base + " address works only once the operator has pointed a wildcard DNS record (*." + base + ") at this server.\n")
		b.WriteString("- Preview links (preview_url, POST .../versions/<n>/preview-link) need a per-site address, which this server does not give sites (409 preview_unavailable): to check a stored version, make it live (PUT .../active-version) and switch back if needed.\n")
	}
	var ways []string
	if f.SignInEmail {
		ways = append(ways, "an emailed code")
	}
	for _, p := range f.SignInProviders {
		if name := map[string]string{"google": "Google", "github": "GitHub"}[p]; name != "" {
			ways = append(ways, name)
		}
	}
	if len(ways) == 0 {
		b.WriteString("- Visitor sign-in: visitors cannot sign in on this server (no email or Google sign-in is set up). Never call SH.requireSignIn() or offer sign-in. Submissions, Personal, Shared boards and private lists are refused (409 visitor_sign_in_unavailable): save to Shared names instead (SH.data(name) with no kind, SH.state, SH.collection).")
	} else {
		b.WriteString("- Visitor sign-in: visitors sign in with " + strings.Join(ways, " or ") + " on a site's own address (a free <name>." + base + " address or a custom domain); a sign-in covers that site only.")
	}
	if f.SharedOrigin {
		b.WriteString(" On the shared address https://" + f.ContentHost + "/ pages save to Shared names without anyone signing in, so anyone who can open a page can change what it saved: never save personal details there.")
	}
	b.WriteString("\n")
	if !f.SignInEmail {
		b.WriteString("- Email: this server sends no email. Nobody signs in with an emailed code (accounts use keys the organiser issues), and Submissions emails are off (notify must be \"off\", 409 email_unavailable).\n")
	}
	contact := f.Contact
	if contact == "" {
		contact = "whoever runs this server"
	}
	b.WriteString("- Help: ask " + contact + "; support@simple-host.app is the hosted service's mailbox, not this server's.\n\n")
	instanceNote = b.String()
}

// controlPlaneSkills name the public instance on purpose: they drive endpoints
// that only exist there. Rewriting them to an event's own hostname would point
// an agent at a box that cannot answer.
var controlPlaneSkills = []string{"run-hackathon", "join-hackathon", "judge-hackathon"}

func controlPlaneSkill(path string) bool {
	for _, name := range controlPlaneSkills {
		if strings.HasPrefix(path, name+"/") || strings.Contains(path, "/"+name+"/") {
			return true
		}
	}
	return false
}
