package web

import (
	"bytes"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/webassets"
)

// CanonicalSiteDomain is the hostname baked into every served text asset: the
// skills, llms.txt, install.html, auth.js and the OpenAPI spec all name it
// literally. That is correct for simple-host.app and wrong everywhere else.
//
// An instance running on another domain has to describe itself, or an agent
// following its documentation publishes to simple-host.app instead. That is not
// a cosmetic bug: a hackathon's entries would land on somebody else's server.
const CanonicalSiteDomain = "simple-host.app"

// RewrittenAssets are served through a handler that substitutes the instance's
// own hostnames, and hidden from the file server so there is exactly one path
// to each. Binary and vendored assets are excluded: nothing in them mentions
// the host, and rewriting bytes we do not own is a bad habit.
var RewrittenAssets = []string{
	"llms.txt",
	"install.html",
	"auth.js",
	"signin.js",
	// features.json is linked from no page but reachable by path, and names
	// simple-host.app throughout.
	"features.json",
	"openapi.yaml",
	"openapi.json",
}

// hostRewriter substitutes this instance's hostnames for the canonical ones.
//
// Nil means "no rewriting needed", which is the case on simple-host.app itself,
// where every replacement would be identity. Callers must handle nil rather
// than paying for a no-op pass over every asset in production.
type hostRewriter struct{ R *strings.Replacer }

// newHostRewriter returns nil when this instance IS the canonical one.
//
// Order matters and is the whole subtlety here: "sites.simple-host.app"
// contains "simple-host.app", so the longer hostnames must be listed first or
// the content host would be Rewritten into "sites.<newdomain>" with the wrong
// prefix. strings.Replacer prefers the earliest-listed match at a position.
func newHostRewriter(siteDomain, contentHost, cnameTarget string) *hostRewriter {
	if siteDomain == "" || siteDomain == CanonicalSiteDomain {
		return nil
	}
	if contentHost == "" {
		contentHost = "sites." + siteDomain
	}
	if cnameTarget == "" {
		cnameTarget = "cname." + siteDomain
	}
	return &hostRewriter{R: strings.NewReplacer(
		// The hosted instance hands out per-person addresses; an instance on
		// another domain keeps the path model (PERSON_HOSTS defaults to off),
		// so its docs describe the path address instead.
		// Per-site addresses (<site>.<handle>.simple-host.app, SITE_HOSTS)
		// become the path address too.
		"<site>.<handle>."+CanonicalSiteDomain+"/", contentHost+"/<handle>/<site>/",
		"<site>.<handle>."+CanonicalSiteDomain, contentHost+"/<handle>/<site>",
		"<sitename>.<handle>."+CanonicalSiteDomain+"/", contentHost+"/<handle>/<sitename>/",
		"<sitename>.<handle>."+CanonicalSiteDomain, contentHost+"/<handle>/<sitename>",
		"<name>.<handle>."+CanonicalSiteDomain+"/", contentHost+"/<handle>/<name>/",
		"<name>.<handle>."+CanonicalSiteDomain, contentHost+"/<handle>/<name>",
		"&lt;site&gt;.&lt;handle&gt;."+CanonicalSiteDomain+"/", contentHost+"/&lt;handle&gt;/&lt;site&gt;/",
		"&lt;site&gt;.&lt;handle&gt;."+CanonicalSiteDomain, contentHost+"/&lt;handle&gt;/&lt;site&gt;",
		"<handle>."+CanonicalSiteDomain+"/v1", contentHost+"/v1",
		"<handle>."+CanonicalSiteDomain+"/<sitename>", contentHost+"/<handle>/<sitename>",
		"<handle>."+CanonicalSiteDomain+"/<site>", contentHost+"/<handle>/<site>",
		"<handle>."+CanonicalSiteDomain+"/<name>", contentHost+"/<handle>/<name>",
		"<handle>."+CanonicalSiteDomain+"/", contentHost+"/<handle>/",
		"<handle>."+CanonicalSiteDomain, contentHost+"/<handle>",
		"&lt;handle&gt;."+CanonicalSiteDomain+"/&lt;site&gt;", contentHost+"/&lt;handle&gt;/&lt;site&gt;",
		"&lt;handle&gt;."+CanonicalSiteDomain+"/", contentHost+"/&lt;handle&gt;/",
		"&lt;handle&gt;."+CanonicalSiteDomain, contentHost+"/&lt;handle&gt;",
		// Every site shares the content host's origin in the path model.
		"(its own browser origin)", "(one browser origin, shared by every site on this server)",
		"sites."+CanonicalSiteDomain, contentHost,
		"cname."+CanonicalSiteDomain, cnameTarget,
		CanonicalSiteDomain, siteDomain,
	)}
}

// Apply rewrites a copy of b. Nil receiver returns b untouched so callers can
// stay branch-free.
func (h *hostRewriter) Apply(b []byte) []byte {
	if h == nil {
		return b
	}
	return []byte(h.R.Replace(string(b)))
}

var assetContentTypes = map[string]string{
	".txt":  "text/plain; charset=utf-8",
	".html": "text/html; charset=utf-8",
	".js":   "application/javascript; charset=utf-8",
	".json": "application/json; charset=utf-8",
	".yaml": "application/yaml; charset=utf-8",
}

// ServeRewrittenAsset serves one embedded asset with the instance's own
// hostnames (rw, which may be nil) and limits substituted in.
func (p *AssetPipeline) ServeRewrittenAsset(name string, rw *hostRewriter, modTime time.Time) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		var err error
		if strings.HasSuffix(name, ".html") {
			// A page gets the shared chrome first, so the rewrite covers it
			// too; BuildChromePage has already put this install's limits in.
			body, err = p.BuildChromePage(name, p.ChromeDataFor(r, ""))
			if err == nil {
				body = rw.Apply(body)
			}
		} else {
			// Both API addresses serve one JSON reference: a product's own
			// composed openapi.json, else the platform's authored YAML,
			// filtered to its public operations.
			sourceName := name
			_, replaced := p.Product.Replace["openapi.json"]
			if name == "openapi.yaml" || name == "openapi.json" {
				sourceName = "openapi.yaml"
				if replaced {
					sourceName = "openapi.json"
				}
			}
			body, err = p.TextFS().ReadFile("static/" + sourceName)
			if err == nil && (name == "openapi.yaml" || name == "openapi.json") && !replaced {
				body, err = webassets.PublicOpenAPISpec(body, "public-host")
			}
			if err == nil && name == "features.json" {
				body = stripBracketMarkers(body, "self", "hosted")
			}
			if err == nil {
				if name == "llms.txt" && rw != nil && p.Product.Hooks.InstanceText == nil {
					body = selfHostedClaudeSetup(body)
				}
				body = []byte(SupportText(string(body)))
				// A product on the hosted service keeps cross-product links.
				// Other installs substitute their own host before the product pass.
				if p.Product.Hooks.InstanceText == nil || p.HostedOwner() == "" {
					body = rw.Apply(body)
				}
				body = p.ProductInstanceText(p.limitsText.apply(body))
			}
			if err == nil && p.Product.Hooks.InstanceText == nil && name == "llms.txt" && InstanceNote != "" {
				body = append([]byte(InstanceNote), body...)
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

// selfHostedClaudeSetup swaps llms.txt's Claude, ChatGPT and Claude Code
// steps on another server: the plugin and the app directories only connect
// to the hosted service, so a self-hosted install has no way to offer them
// and must describe the URL-only path instead (mirrors install.html's
// sh:self panels for the same three apps).
func selfHostedClaudeSetup(body []byte) []byte {
	body = replaceSetupLine(body, "CLAUDE SETUP: ", "CLAUDE SETUP: open Settings, then Connectors, then Add custom connector. Name it Simple Host, address https://simple-host.app/mcp, choose Add, then Connect, sign in, then choose Allow. Works on web, desktop, and phone. Claude's connector directory and the Simple Host plugin connect to the hosted service, not this server.")
	body = replaceSetupLine(body, "CHATGPT SETUP: ", "CHATGPT SETUP: open Settings, then Apps & Connectors, then Advanced settings, and turn on Developer mode. Back in Apps & Connectors choose Create, name it Simple Host, paste https://simple-host.app/mcp as the MCP server URL, choose OAuth, then Create. Sign in with Google or an email code, then choose Allow. In a chat, pick Simple Host from the + menu, or just ask. ChatGPT's plugin directory connects to the hosted service, not this server; when Simple Host adds features, open the connector's settings and refresh it.")
	return bytes.Replace(body, []byte(" Claude Code: install the plugin (it carries the connector), then /mcp, Authenticate."), []byte(" Claude Code: claude mcp add --transport http simple-host https://simple-host.app/mcp, then /mcp, Authenticate."), 1)
}

// replaceSetupLine swaps the single line beginning with prefix (up to the
// next newline) for replacement. A missing prefix leaves body untouched.
func replaceSetupLine(body []byte, prefix, replacement string) []byte {
	start := bytes.Index(body, []byte(prefix))
	if start < 0 {
		return body
	}
	end := bytes.IndexByte(body[start:], '\n')
	if end < 0 {
		end = len(body) - start
	}
	return append(append(append([]byte{}, body[:start]...), []byte(replacement)...), body[start+end:]...)
}

// InstanceHosts rewrites the canonical hostnames baked into served assets to
// this instance's own. Nil on simple-host.app, where every substitution is
// identity.
//
// It is a package var, not an AssetPipeline field, because address settings
// are instance-wide, not edition-bound. SetInstanceHosts sets it at startup,
// before the server accepts a request.
var InstanceHosts *hostRewriter

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
	InstanceHosts = newHostRewriter(siteDomain, contentHost, cnameTarget)
	instanceIsCanonical = siteDomain == "" || siteDomain == CanonicalSiteDomain
	InstanceIsHostedService = siteDomain == CanonicalSiteDomain
	InstanceSiteDomain = siteDomain
}

// InstanceSiteDomain is this install's SITE_DOMAIN ("" before setup).
var InstanceSiteDomain string

// InstanceIsHostedService: this process is simple-host.app itself, not an
// install that has no domain yet. Gates copy that names who runs the service.
var InstanceIsHostedService bool

// instanceIsCanonical: this is the hosted service (or has no domain), which
// the shared text describes, so llms.txt needs no THIS SERVER note.
var instanceIsCanonical = true

// InstanceFacts is what this install's llms.txt says about itself, above the
// text every install shares (which describes simple-host.app).
type InstanceFacts struct {
	// SiteDomain is also where free names are (never SITE_BASE_DOMAIN).
	SiteDomain, ContentHost string
	// SharedOrigin: every site is served on the content host's one origin
	// (PERSON_HOSTS and SITE_HOSTS off, as on a small box).
	SharedOrigin bool
	// SignInEmail and SignInProviders: how visitors can sign in. SignInEmail
	// means email-code sign-in works (EMAIL_SIGNIN and a sender), unlike
	// EmailCapable.
	SignInEmail     bool
	SignInProviders []string
	// OIDCLabel names the company sign-in button (OIDC_LABEL) when "oidc"
	// is in SignInProviders.
	OIDCLabel string
	// EmailCapable: this install can send email at all (RESEND_API_KEY),
	// regardless of EMAIL_SIGNIN.
	EmailCapable bool
	// Contact: who to ask for help (auth.SupportContact).
	Contact string
}

// InstanceNote is prepended to llms.txt on an install other than
// simple-host.app (SetInstanceNote); "" there.
var InstanceNote string

// CurrentInstance is the facts SetInstanceNote was given, for the auth docs.
var CurrentInstance InstanceFacts

// SetInstanceNote writes the THIS SERVER block of llms.txt from f. Call once
// at startup, after SetInstanceHosts; it does nothing on simple-host.app.
func SetInstanceNote(f InstanceFacts) {
	CurrentInstance = f
	if InstanceHosts == nil || instanceIsCanonical {
		InstanceNote = ""
		return
	}
	base := f.SiteDomain
	var b strings.Builder
	b.WriteString("THIS SERVER (" + f.SiteDomain + ") — read this first: the text after it describes the hosted service at simple-host.app, and where it differs, what is written here is what holds on this server.\n")
	if f.SharedOrigin {
		b.WriteString("- Addresses: every site is at https://" + f.ContentHost + "/<handle>/<site>/, and every site on this server shares that one browser origin: localStorage, sessionStorage, IndexedDB and cookies are shared with every other site here. Keep nothing private in the browser, and prefix browser storage keys with the site's name. A free <name>." + base + " address works only once the operator has pointed a wildcard DNS record (*." + base + ") at this server.\n")
		b.WriteString("- Preview links (preview_url, POST .../versions/<n>/preview-link) need a per-site address, which this server does not give sites (409 preview_unavailable): to check a stored version, make it live (PUT .../active-version) and switch back if needed.\n")
	}
	ways := signInWays(f.SignInEmail, f, "")
	if len(ways) == 0 {
		b.WriteString("- Visitor sign-in: visitors cannot sign in on this server (no email or Google sign-in is set up). Never call SH.requireSignIn() or offer sign-in, and give storage only rules that need no signed-in visitor.")
	} else {
		b.WriteString("- Visitor sign-in: visitors sign in with " + strings.Join(ways, " or ") + " on a site's own address (a free <name>." + base + " address or a custom domain); a sign-in covers that site only.")
	}
	if f.SharedOrigin {
		b.WriteString(" On the shared address https://" + f.ContentHost + "/ nobody signs in, so a save there is open to anyone the storage rules let in: never save personal details there.")
	}
	b.WriteString("\n")
	if !f.EmailCapable {
		b.WriteString("- Email: this server sends no email. Nobody signs in with an emailed code (accounts use keys the organiser issues).\n")
	} else if !f.SignInEmail {
		b.WriteString("- Email: email sign-in is off on this server (EMAIL_SIGNIN=off). Visitors sign in with their company account instead.\n")
	}
	contact := f.Contact
	if contact == "" {
		contact = "whoever runs this server"
	}
	b.WriteString("- Help: ask " + contact + "; support@simple-host.app is the hosted service's mailbox, not this server's.\n\n")
	InstanceNote = b.String()
}

// signInWays names each way a person can sign in, in the docs' words;
// oidcSuffix follows the company sign-in label.
func signInWays(email bool, f InstanceFacts, oidcSuffix string) []string {
	var ways []string
	if email {
		ways = append(ways, "an emailed code")
	}
	for _, provider := range f.SignInProviders {
		switch provider {
		case "google":
			ways = append(ways, "Google")
		case "github":
			ways = append(ways, "GitHub")
		case "oidc":
			label := f.OIDCLabel
			if label == "" {
				label = "your company account"
			}
			ways = append(ways, label+oidcSuffix)
		}
	}
	return ways
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
