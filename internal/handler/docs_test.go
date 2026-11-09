package handler

import (
	"bytes"
	"encoding/json"
	"html"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/webassets"
)

func TestDocsRoutes(t *testing.T) {
	for _, kind := range []edition.Kind{edition.Host, edition.Hack} {
		t.Run(edition.ForKind(kind).Presentation().ProductName, func(t *testing.T) {
			mux := chromeTestMux(t, kind)
			for _, route := range []string{"/docs", "/docs.html"} {
				r := get(t, mux, "", route)
				if r.Code != 301 || r.Header().Get("Location") != "/docs/" {
					t.Errorf("%s: %d, Location %q", route, r.Code, r.Header().Get("Location"))
				}
			}
			for _, route := range webassets.DocsPaths(kind == edition.Hack) {
				r := get(t, mux, "", route)
				if r.Code != 200 || !strings.HasPrefix(r.Header().Get("Content-Type"), "text/html") {
					t.Fatalf("%s: %d %s", route, r.Code, r.Header().Get("Content-Type"))
				}
				body := r.Body.String()
				for _, want := range []string{`<header class="sh-header`, `class="sh-footer"`, `id="theme-toggle"`} {
					if strings.Count(body, want) != 1 {
						t.Errorf("%s: expected one %s", route, want)
					}
				}
				footer := body[strings.Index(body, `<footer class="sh-footer"`):]
				if strings.Count(footer, `href="/docs/" aria-current="page"`) != 1 {
					t.Errorf("%s: footer docs link not current", route)
				}
				if strings.Contains(body, `href="/docs/docs/"`) || strings.Contains(body, `href="/hack-llms.txt"`) {
					t.Errorf("%s: invalid public documentation link", route)
				}
				if strings.Contains(body, "<!--sh:") {
					t.Errorf("%s: unresolved marker", route)
				}
				for _, script := range docsScriptRE.FindAllStringSubmatch(body, -1) {
					if !docsScriptSrcRE.MatchString(script[1]) {
						t.Errorf("%s: inline script", route)
					}
				}
				if !strings.Contains(r.Header().Get("Content-Security-Policy"), "script-src 'self' 'wasm-unsafe-eval'") {
					t.Errorf("%s: missing UI CSP", route)
				}
				brand := "Simple Host"
				if kind == edition.Hack {
					brand = "Simple Hack"
				}
				if !strings.Contains(body, brand) {
					t.Errorf("%s: missing %s", route, brand)
				}
			}
			for route, mime := range map[string]string{"/docs/pagefind/pagefind.js": "javascript", "/docs/old-anchors.js": "javascript"} {
				r := get(t, mux, "", route)
				if r.Code != 200 || !strings.Contains(r.Header().Get("Content-Type"), mime) || r.Header().Get("Cache-Control") == "" {
					t.Errorf("asset %s: %d %v", route, r.Code, r.Header())
				}
			}
			for _, route := range []string{"/docs/unknown/", "/docs/missing.js"} {
				if r := get(t, mux, "", route); r.Code != 404 {
					t.Errorf("%s: %d", route, r.Code)
				}
			}
			if kind == edition.Hack {
				body := get(t, mux, "", "/docs/webhooks/").Body.String()
				if strings.Contains(body, "site.deployed") || strings.Contains(body, "visitor.signed_up") {
					t.Error("Host events leaked into Hack")
				}
			}
		})
	}
}

func TestDocsRuntimeText(t *testing.T) {
	t.Setenv("SITE_STORAGE_MAX_BYTES", "2345678")
	t.Setenv("SITE_STORAGE_FILE_MAX_BYTES", "123456")
	oldHosted, oldCanonical := instanceIsHostedService, instanceIsCanonical
	oldHosts, oldDomain, oldBase := instanceHosts, instanceSiteDomain, currentBaseTextTo()
	t.Cleanup(func() {
		instanceIsHostedService, instanceIsCanonical = oldHosted, oldCanonical
		instanceHosts, instanceSiteDomain = oldHosts, oldDomain
		baseTextMu.Lock()
		baseTextTo = oldBase
		baseTextMu.Unlock()
		clearAssetText()
		chromeCache.Clear()
	})
	instanceIsHostedService = true
	host := newAssetPipeline(edition.ForKind(edition.Host), "")
	host.docs.once.Do(host.buildDocsCache)
	if !bytes.Contains(host.docs.pages["guides/agents/index.html"], []byte("npx skills add")) {
		t.Fatal("hosted instructions absent")
	}
	SetInstanceHosts("docs-box.example", "sites.docs-box.example", "")
	limits := withLimits(t, map[string]string{"MAX_SITES_PER_ACCOUNT": "250", "DOMAIN_UNPROVEN_HOURS": "36"})
	for _, kind := range []edition.Kind{edition.Host, edition.Hack} {
		p := newAssetPipeline(edition.ForKindWithLimits(kind, limits), "https://docs-box.example")
		p.docs.once.Do(p.buildDocsCache)
		if p.docs.err != nil {
			t.Fatal(p.docs.err)
		}
		body := string(p.docs.pages["guides/agents/index.html"])
		if strings.Contains(body, "npx skills add") || !strings.Contains(body, "this install") {
			t.Error("hosted/self blocks were not resolved")
		}
		if strings.Contains(body, "https://simple-host.app/mcp") || strings.Contains(body, "https://simple-hack.app/mcp") {
			t.Error("hosted instance URL leaked")
		}
		if kind == edition.Host && strings.Contains(string(p.docs.pages["guides/addresses/index.html"]), "take-a-free-short-name") {
			t.Error("hosted-only address task or TOC link leaked into self-hosted docs")
		}
		limitsBody := string(p.docs.pages["limits/index.html"])
		if kind == edition.Host && !strings.Contains(limitsBody, "Up to 250 sites") {
			t.Error("limit wording was not rewritten")
		}
		if !strings.Contains(limitsBody, "Up to 2,345,678 bytes") || !strings.Contains(limitsBody, "Up to 123,456 bytes per file") {
			t.Error("storage byte allowances were not rewritten")
		}
	}
}

// Inline only the fetched shared scripts for older chrome/theme assertions.
// The actual served page is separately required to have src on every script.
func inlineDocsChromeForTest(t *testing.T, mux http.Handler, body string) string {
	t.Helper()
	return docsScriptRE.ReplaceAllStringFunc(body, func(tag string) string {
		m := docsScriptRE.FindStringSubmatch(tag)
		prefix := `src="/docs/_chrome/`
		i := strings.Index(m[1], prefix)
		if i < 0 {
			return tag
		}
		src := "/docs/_chrome/" + strings.Split(m[1][i+len(prefix):], `"`)[0]
		r := get(t, mux, "", src)
		if r.Code != 200 {
			t.Fatalf("chrome script %s: %d", src, r.Code)
		}
		return "<script>" + r.Body.String() + "</script>"
	})
}

func TestDocsChromeScriptsAfterRestart(t *testing.T) {
	p := newAssetPipeline(edition.ForKind(edition.Host), "")
	p.docs.once.Do(p.buildDocsCache)
	fresh := newAssetPipeline(edition.ForKind(edition.Host), "")
	for name, body := range p.docs.scripts {
		r := httptest.NewRecorder()
		fresh.serveDocs(r, httptest.NewRequest("GET", "/docs/"+name, nil))
		if r.Code != 200 || !bytes.Equal(r.Body.Bytes(), body) {
			t.Errorf("cached script %s unavailable after restart", name)
		}
	}
}

func TestDocsSitemap(t *testing.T) {
	for _, hack := range []bool{false, true} {
		r := httptest.NewRecorder()
		serveSitemapXML(r, httptest.NewRequest("GET", "https://example.org/sitemap.xml", nil), hack)
		for _, route := range webassets.DocsPaths(hack) {
			if !strings.Contains(r.Body.String(), route+"</loc>") {
				t.Errorf("sitemap missing %s", route)
			}
		}
		if hack && strings.Contains(r.Body.String(), "/docs/guides/addresses/") {
			t.Error("Hack sitemap includes Host-only guide")
		}
	}
}

func TestDocsOldAnchors(t *testing.T) {
	// Run the actual external script against a tiny location stub, offline.
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable for anchor compatibility test")
	}
	data, err := fs.ReadFile(webassets.DocsTree(false), "old-anchors.js")
	if err != nil {
		t.Fatal(err)
	}
	targets := map[string]string{
		"install-skills": "/install.html", "connect-a-domain": "/docs/guides/addresses/#connect-your-own-domain",
		"address-families": "/docs/guides/addresses/", "add-photos": "/docs/guides/dashboard/#add-photos",
		"storage-details": "/docs/guides/storage/#write-to-storage-from-your-own-code", "notifications": "/docs/guides/notifications/",
		"swagger-ui": "/docs/api/", "verify-before-processing": "/docs/webhooks/#verify-before-processing", "unknown": "",
	}
	for anchor, want := range targets {
		encoded, _ := json.Marshal(anchor)
		js := `let dest='';global.addEventListener=()=>{};global.location={hash:'#'+` + string(encoded) + `,replace:(x)=>dest=x};` + string(data) + `;process.stdout.write(dest);`
		out, err := exec.Command("node", "-e", js).CombinedOutput()
		if err != nil || string(out) != want {
			t.Errorf("%s: %s %v, want %s", anchor, out, err, want)
		}
	}
}

func TestDocsOldAnchorHashChange(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	data, err := fs.ReadFile(webassets.DocsTree(false), "old-anchors.js")
	if err != nil {
		t.Fatal(err)
	}
	js := `let listener,dest='';global.addEventListener=(type,fn)=>{if(type==='hashchange')listener=fn};global.location={hash:'#unknown',replace:x=>dest=x};` + string(data) + `;location.hash='#notifications';listener();process.stdout.write(dest);`
	out, err := exec.Command("node", "-e", js).CombinedOutput()
	if err != nil || string(out) != "/docs/guides/notifications/" {
		t.Fatalf("hashchange: %s %v", out, err)
	}
}

func TestDocsConnectorMetadata(t *testing.T) {
	for _, domain := range []string{"simple-host.app", "simple-hack.app"} {
		h := &ConnectorHandler{issuer: "https://" + domain}
		for key, serve := range map[string]http.HandlerFunc{"resource_documentation": h.protectedResourceMetadata, "service_documentation": h.authorizationServerMetadata} {
			r := httptest.NewRecorder()
			serve(r, httptest.NewRequest("GET", "/", nil))
			var body map[string]any
			if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body[key] != h.issuer+"/docs/" {
				t.Errorf("%s: %v", key, body[key])
			}
		}
	}
}

func TestDocsNotInStaticTree(t *testing.T) {
	if _, err := fs.Stat(webassets.Static, "static/docs-site/host/index.html"); !os.IsNotExist(err) {
		t.Fatalf("docs exposed in plain static tree: %v", err)
	}
}

func TestDocsAnchorTargetsExist(t *testing.T) {
	for _, hack := range []bool{false, true} {
		tree := webassets.DocsTree(hack)
		script, err := fs.ReadFile(tree, "old-anchors.js")
		if err != nil {
			t.Fatal(err)
		}
		match := regexp.MustCompile(`const targets = (\{[^;]+\});`).FindSubmatch(script)
		if match == nil {
			t.Fatal("missing generated anchor map")
		}
		var targets map[string]string
		if err := json.Unmarshal(match[1], &targets); err != nil {
			t.Fatal(err)
		}
		if len(targets) < 15 {
			t.Fatal("incomplete anchor map")
		}
		for anchor, target := range targets {
			if anchor == "install-skills" {
				if target != "/install.html" {
					t.Errorf("hack=%v: install-skills target = %q", hack, target)
				}
				if _, err := fs.Stat(webassets.Static, "static/install.html"); err != nil {
					t.Fatal(err)
				}
				continue
			}
			u, err := url.Parse(target)
			if err != nil || !strings.HasPrefix(u.Path, "/docs/") {
				t.Fatalf("invalid target %q", target)
			}
			page, err := fs.ReadFile(tree, strings.TrimPrefix(u.Path, "/docs/")+"index.html")
			if err != nil {
				t.Errorf("hack=%v #%s: %v", hack, anchor, err)
				continue
			}
			if u.Fragment != "" && !regexp.MustCompile(`<h[1-6]\b[^>]*\bid="`+regexp.QuoteMeta(u.Fragment)+`"`).Match(page) {
				t.Errorf("hack=%v #%s: missing heading %s", hack, anchor, target)
			}
		}
	}
}

func TestDocsDirectoryRedirectQuery(t *testing.T) {
	for _, kind := range []edition.Kind{edition.Host, edition.Hack} {
		r := get(t, chromeTestMux(t, kind), "", "/docs/guides/storage?q=hello%20world&from=old")
		if r.Code != 301 || r.Header().Get("Location") != "/docs/guides/storage/?q=hello%20world&from=old" {
			t.Fatalf("redirect: %d %q", r.Code, r.Header().Get("Location"))
		}
	}
}

func TestDocsHostPublicBaseURL(t *testing.T) {
	oldHosted, oldCanonical := instanceIsHostedService, instanceIsCanonical
	oldHosts, oldDomain, oldBase := instanceHosts, instanceSiteDomain, currentBaseTextTo()
	t.Cleanup(func() {
		instanceIsHostedService, instanceIsCanonical = oldHosted, oldCanonical
		instanceHosts, instanceSiteDomain = oldHosts, oldDomain
		baseTextMu.Lock()
		baseTextTo = oldBase
		baseTextMu.Unlock()
		clearAssetText()
		chromeCache.Clear()
	})
	t.Setenv("SITE_DOMAIN", "box.example")
	t.Setenv("PUBLIC_BASE_URL", "http://box.example:8080")
	SetInstanceHosts("box.example", "sites.box.example", "")
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, os.Getenv("PUBLIC_BASE_URL"), chromeTestHandler(edition.Host))
	for route, want := range map[string]string{
		"/docs/start/api/":     "http://box.example:8080/v1/me",
		"/docs/guides/agents/": "http://box.example:8080/mcp",
		"/docs/start/pages/":   "http://box.example:8080/auth.js",
		"/docs/guides/access/": "http://box.example:8080/auth.js",
	} {
		r := get(t, mux, "box.example:8080", route)
		// Syntax highlighting can split a URL across spans.
		text := html.UnescapeString(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(r.Body.String(), ""))
		if r.Code != 200 || !strings.Contains(text, want) || strings.Contains(text, "https://box.example/") {
			t.Errorf("%s: missing full public URL %s", route, want)
		}
	}
	t.Run("address family legacy anchor on self-hosted Host", func(t *testing.T) {
		if _, err := exec.LookPath("node"); err != nil {
			t.Skip("node unavailable for anchor compatibility test")
		}
		script := get(t, mux, "box.example:8080", "/docs/old-anchors.js")
		if script.Code != 200 {
			t.Fatalf("anchor script: %d", script.Code)
		}
		js := `let dest='';global.addEventListener=()=>{};global.location={hash:'#address-families',replace:x=>dest=x};` + script.Body.String() + `;process.stdout.write(dest);`
		out, err := exec.Command("node", "-e", js).CombinedOutput()
		if err != nil || string(out) != "/docs/guides/addresses/" {
			t.Fatalf("self-hosted anchor: %s %v", out, err)
		}
		page := get(t, mux, "box.example:8080", string(out))
		if page.Code != 200 || !strings.Contains(page.Body.String(), `id="connect-your-own-domain"`) {
			t.Fatalf("legacy target did not serve the addresses guide: %d", page.Code)
		}
		if strings.Contains(page.Body.String(), "put-every-site-under-one-domain") || strings.Contains(page.Body.String(), "<!--sh:hosted-->") {
			t.Fatal("served self-hosted page still contains hosted-only heading or marker")
		}
	})
}

func TestDocsBrandAndSearchCopy(t *testing.T) {
	for _, hack := range []bool{false, true} {
		tree := webassets.DocsTree(hack)
		err := fs.WalkDir(tree, ".", func(name string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(name, ".html") {
				return err
			}
			body, err := fs.ReadFile(tree, name)
			if err != nil {
				return err
			}
			for _, block := range regexp.MustCompile(`(?s)<!--sh:(?:hosted|self)-->(.*?)<!--/sh:(?:hosted|self)-->`).FindAllSubmatch(body, -1) {
				if bytes.HasPrefix(bytes.TrimSpace(block[1]), []byte("<li")) {
					continue
				} // TOC links, outside Pagefind body.
				if !bytes.Contains(block[1], []byte("data-pagefind-ignore")) {
					t.Errorf("%s: instance text indexed", name)
				}
			}
			if hack && (bytes.Contains(body, []byte("Sites per account")) || bytes.Contains(body, []byte("Follow Connect Domain"))) {
				t.Errorf("%s: Host-only instructions on Hack", name)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestFeaturesConnectDomainLink(t *testing.T) {
	for _, kind := range []edition.Kind{edition.Host, edition.Hack} {
		body := get(t, chromeTestMux(t, kind), "", "/features.html").Body.String()
		has := strings.Contains(body, `href="/skills/connect-domain/SKILL.md"`)
		if has != (kind == edition.Host) {
			t.Errorf("%v: connect-domain link present=%v", kind, has)
		}
	}
}
