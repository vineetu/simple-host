package handler

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/webassets"
)

type docsCache struct {
	baseURL string
	once    sync.Once
	pages   map[string][]byte
	scripts map[string][]byte
	md      map[string][]byte
	err     error
}

var docsScriptRE = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)
var docsScriptSrcRE = regexp.MustCompile(`(?i)\bsrc\s*=`)

// docsRuntimeText is the one set of runtime text passes every served docs
// page gets, HTML and .md alike: the baseURL origin swap, storage limits,
// hosted/self and hack/no-hack block resolution, and (self-hosted only) the
// chrome-wide self-hosted rewrite. HTML additionally wraps this with
// withChrome beforehand and script externalization afterward
// (buildDocsCache); kept as one function so the two paths cannot drift, which
// is how the .md copy once shipped simple-host.app names a self-hosted
// install's HTML had already rewritten.
func (p *assetPipeline) docsRuntimeText(body []byte) []byte {
	// Replace the full origin before the hostname-only chrome pass, as
	// discovery documents do, preserving PUBLIC_BASE_URL's scheme and port.
	if !p.pres.HackAssets && p.docs.baseURL != "" && p.hostedOwner() == "" {
		body = bytes.ReplaceAll(body, []byte("https://simple-host.app"), []byte(p.docs.baseURL))
	}
	body = p.docsStorageLimits(body)
	body = hostedBlocks(p.HackInstanceText(p.limitsText.apply(body)), p.hostedOwner() != "")
	body = hasHackBlocks(body, p.hostedOwner() != "" || p.pres.HackAssets)
	if p.hostedOwner() == "" {
		body = p.selfHostedChromeRewrite(body)
	}
	return body
}

// buildDocsCache uses the same chrome and text passes as chromePage. All pages
// are assembled once per immutable asset pipeline, so cached HTML can fetch its
// chrome scripts even immediately after a process restart.
func (p *assetPipeline) buildDocsCache() {
	p.docs.pages = make(map[string][]byte)
	p.docs.scripts = make(map[string][]byte)
	p.docs.md = make(map[string][]byte)
	d := p.chromeDataFor(&http.Request{URL: &url.URL{Path: "/docs/"}}, "")
	p.docs.err = fs.WalkDir(webassets.DocsTree(p.pres.HackAssets), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if strings.HasSuffix(name, ".md") {
			body, err := fs.ReadFile(webassets.DocsTree(p.pres.HackAssets), name)
			if err != nil {
				return err
			}
			// Same runtime text passes as the HTML (docsRuntimeText): a
			// .md copy carries no chrome and needs no script
			// externalization, but still needs every text rewrite, including
			// the self-hosted one, or a self-hosted install's .md serves
			// simple-host.app names its HTML had already rewritten.
			body = p.docsRuntimeText(body)
			p.docs.md[name] = body
			return nil
		}
		if !strings.HasSuffix(name, ".html") {
			return nil
		}
		body, err := fs.ReadFile(webassets.DocsTree(p.pres.HackAssets), name)
		if err != nil {
			return err
		}
		body, err = withChrome(body, d)
		if err != nil {
			return err
		}
		body = p.docsRuntimeText(body)
		// Astro scripts are externalized at build time. The remaining scripts
		// belong to the shared chrome; preserve their exact bodies and order.
		body = docsScriptRE.ReplaceAllFunc(body, func(tag []byte) []byte {
			parts := docsScriptRE.FindSubmatch(tag)
			if docsScriptSrcRE.Match(parts[1]) {
				return tag
			}
			sum := sha256.Sum256(parts[2])
			src := fmt.Sprintf("_chrome/%x.js", sum)
			p.docs.scripts[src] = bytes.Clone(parts[2])
			return []byte(fmt.Sprintf(`<script%s src="/docs/%s"></script>`, parts[1], src))
		})
		p.docs.pages[name] = body
		return nil
	})
}

func (p *assetPipeline) serveDocs(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/docs/")
	if name == "" || strings.HasSuffix(name, "/") {
		name += "index.html"
	}
	if !fs.ValidPath(name) {
		http.NotFound(w, r)
		return
	}
	p.docs.once.Do(p.buildDocsCache)
	if p.docs.err != nil {
		http.Error(w, "Documentation could not be loaded", http.StatusInternalServerError)
		return
	}
	if strings.HasSuffix(name, ".md") {
		body, ok := p.docs.md[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		if r.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
		return
	}
	if body, ok := p.docs.pages[name]; ok {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		if r.Method != http.MethodHead {
			_, _ = w.Write(stampNonce(r, body))
		}
		return
	}
	body, ok := p.docs.scripts[name]
	if !ok {
		var err error
		body, err = fs.ReadFile(webassets.DocsTree(p.pres.HackAssets), name)
		if err != nil {
			// Canonical directory URLs, without exposing directory listings.
			if _, err := fs.Stat(webassets.DocsTree(p.pres.HackAssets), name+"/index.html"); err == nil {
				target := r.URL.Path + "/"
				if r.URL.RawQuery != "" {
					target += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, target, http.StatusMovedPermanently)
				return
			}
			http.NotFound(w, r)
			return
		}
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeContent(w, r, path.Base(name), time.Time{}, bytes.NewReader(body))
}
