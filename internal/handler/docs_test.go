package handler

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"github.com/vsriram/simple-host/internal/config"
	"html"
	"io"
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
	for _, kind := range []*edition.Edition{platformTestEdition()} {
		t.Run(kind.Presentation().ProductName, func(t *testing.T) {
			mux := chromeTestMux(t, kind)
			for _, route := range []string{"/docs", "/docs.html"} {
				r := get(t, mux, "", route)
				if r.Code != 301 || r.Header().Get("Location") != "/docs/" {
					t.Errorf("%s: %d, Location %q", route, r.Code, r.Header().Get("Location"))
				}
			}
			for _, route := range docsPaths(webassets.DocsTree()) {
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
				if kind.Identity().TeamKeys {
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
			if kind.Identity().TeamKeys {
				body := get(t, mux, "", "/docs/webhooks/").Body.String()
				if strings.Contains(body, "site.deployed") || strings.Contains(body, "visitor.signed_up") {
					t.Error("Host events leaked into Hack")
				}
			}
		})
	}
}

func TestDocsMarkdownCopies(t *testing.T) {
	for _, kind := range []*edition.Edition{platformTestEdition()} {
		mux := chromeTestMux(t, kind)
		r := get(t, mux, "", "/docs/index.md")
		if r.Code != 200 || !strings.HasPrefix(r.Header().Get("Content-Type"), "text/markdown") {
			t.Fatalf("/docs/index.md: %d %s", r.Code, r.Header().Get("Content-Type"))
		}
		if !strings.HasPrefix(strings.TrimSpace(r.Body.String()), "# Docs") {
			t.Errorf("/docs/index.md: expected a leading H1, got %q", r.Body.String()[:40])
		}
		if strings.Contains(r.Body.String(), "<LinkCard") || strings.Contains(r.Body.String(), "import ") {
			t.Error("/docs/index.md: unconverted MDX component or import survived")
		}
		if r := get(t, mux, "", "/docs/guides/storage.md"); r.Code != 200 {
			t.Errorf("/docs/guides/storage.md: %d", r.Code)
		}
		if r := get(t, mux, "", "/docs/unknown.md"); r.Code != 404 {
			t.Errorf("/docs/unknown.md: %d", r.Code)
		}
	}
}

func TestMovingSavedDataDocs(t *testing.T) {
	mux := chromeTestMux(t, platformTestEdition())
	for _, route := range []string{"/docs/guides/moving-saved-data/", "/docs/guides/moving-saved-data.md"} {
		r := get(t, mux, "", route)
		if r.Code != http.StatusOK {
			t.Fatalf("%s: %d", route, r.Code)
		}
		body := r.Body.String()
		for _, want := range []string{"Move a site off the older saved data", "A form only you read", "One entry per person", "Other older saved data"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %q", route, want)
			}
		}
		plain := html.UnescapeString(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(body, ""))
		plain = strings.ReplaceAll(plain, "’", "'")
		plain = strings.Join(strings.Fields(plain), " ")
		for _, want := range []string{"CREATE UNIQUE INDEX entries_one_per_person", "row_conflict", "Owner tools bypass row rules", "Keep each person's private export", "preserving signed-in visitors' existing writes"} {
			if !strings.Contains(plain, want) {
				t.Errorf("%s: missing migration requirement %q", route, want)
			}
		}
		for _, private := range []string{"Personal paragraphs for Email 2", "finance-ai-prompts", "sports-effect-lab", "Draft for R4-N"} {
			if strings.Contains(body, private) {
				t.Errorf("%s: draft-only text %q", route, private)
			}
		}
	}
	if home := get(t, mux, "", "/docs/").Body.String(); !strings.Contains(home, `href="/docs/guides/moving-saved-data/"`) {
		t.Fatal("moving guide absent from docs listing")
	}
	for _, route := range []string{"/docs/self-hosting/settings/storage-migration/", "/docs/self-hosting/settings/storage-migration.md"} {
		body := get(t, mux, "", route).Body.String()
		if strings.Contains(strings.ToLower(body), "no removal date") || strings.Contains(body, "Keep the existing API") {
			t.Errorf("%s: obsolete migration advice", route)
		}
		if !strings.Contains(body, "STORAGE_SWITCH_AT") || !strings.Contains(body, "/docs/guides/moving-saved-data") {
			t.Errorf("%s: missing switch setting or moving guide", route)
		}
	}
}

func TestMovingSavedDataInstanceText(t *testing.T) {
	oldHosted, oldCanonical := instanceIsHostedService, instanceIsCanonical
	oldHosts, oldDomain := instanceHosts, instanceSiteDomain
	t.Cleanup(func() {
		instanceIsHostedService, instanceIsCanonical = oldHosted, oldCanonical
		instanceHosts, instanceSiteDomain = oldHosts, oldDomain
		clearAssetText()
		chromeCache.Clear()
	})
	for _, tc := range []struct {
		name, switchAt string
		hosted         bool
	}{
		{name: "hosted", hosted: true, switchAt: "2026-12-17T17:00:00Z"},
		{name: "self with another date", switchAt: "2027-03-01T12:00:00Z"},
		{name: "self without a date"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("STORAGE_SWITCH_AT", tc.switchAt)
			SetInstanceHosts("docs-box.example", "sites.docs-box.example", "")
			instanceIsHostedService = tc.hosted
			p := testAssetPipeline(edition.Default(config.DefaultLimits()), "https://docs-box.example")
			for _, route := range []string{"/docs/guides/moving-saved-data/", "/docs/guides/moving-saved-data.md"} {
				r := httptest.NewRecorder()
				p.serveDocs(r, httptest.NewRequest("GET", route, nil))
				body := r.Body.String()
				if r.Code != http.StatusOK {
					t.Fatalf("%s: %d", route, r.Code)
				}
				if strings.Contains(body, "17 December 2026") != tc.hosted {
					t.Errorf("%s: hosted date present=%v", route, !tc.hosted)
				}
				if !tc.hosted && (!strings.Contains(body, "STORAGE_SWITCH_AT") || !strings.Contains(body, "no switch date is set")) {
					t.Errorf("%s: missing operator-configured date instructions", route)
				}
			}
		})
	}
}

func TestMovingSavedDataSearchText(t *testing.T) {
	// Inspect the actual Pagefind fragment used for search results, including
	// excerpts; a runtime-only HTML rewrite cannot remove indexed copy.
	tree := webassets.DocsTree()
	names, err := fs.Glob(tree, "pagefind/fragment/*.pf_fragment")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		data, err := fs.ReadFile(tree, name)
		if err != nil {
			t.Fatal(err)
		}
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		data, err = io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		var fragment struct{ URL, Content string }
		if err := json.Unmarshal(bytes.TrimPrefix(data, []byte("pagefind_dcd")), &fragment); err != nil {
			t.Fatal(err)
		}
		if fragment.URL != "/guides/moving-saved-data/" {
			continue
		}
		for _, private := range []string{"17 December 2026", "STORAGE_SWITCH_AT", "This install's operator"} {
			if strings.Contains(fragment.Content, private) {
				t.Errorf("instance-specific text %q indexed by Pagefind", private)
			}
		}
		if !strings.Contains(fragment.Content, "One entry per person") {
			t.Fatal("shared guide instructions absent from search")
		}
		return
	}
	t.Fatal("moving guide absent from Pagefind fragments")
}

func TestDocsMarkdownRuntimeText(t *testing.T) {
	oldHosted, oldCanonical := instanceIsHostedService, instanceIsCanonical
	oldHosts, oldDomain := instanceHosts, instanceSiteDomain
	t.Cleanup(func() {
		instanceIsHostedService, instanceIsCanonical = oldHosted, oldCanonical
		instanceHosts, instanceSiteDomain = oldHosts, oldDomain
		clearAssetText()
		chromeCache.Clear()
	})
	instanceIsHostedService = true
	host := testAssetPipeline(edition.Default(config.DefaultLimits()), "")
	host.docs.once.Do(host.buildDocsCache)
	if !bytes.Contains(host.docs.md["guides/agents.md"], []byte("npx skills add")) {
		t.Fatal("hosted instructions absent from the .md copy")
	}
	for _, body := range [][]byte{host.docs.pages["self-hosting/settings/storage-migration/index.html"], host.docs.md["self-hosting/settings/storage-migration.md"]} {
		for _, want := range []string{"17 December 2026, 17:00 UTC", "have been converted automatically to rules", "If you were notified", "you must set its rules before the switch", "lose access to it"} {
			if !bytes.Contains(body, []byte(want)) {
				t.Errorf("hosted migration docs missing %q", want)
			}
		}
		if bytes.Contains(body, []byte("for every owner")) {
			t.Error("hosted migration docs still promise conversion for every owner")
		}
	}
	SetInstanceHosts("docs-box.example", "sites.docs-box.example", "")
	self := testAssetPipeline(edition.Default(config.DefaultLimits()), "https://docs-box.example")
	self.docs.once.Do(self.buildDocsCache)
	body := self.docs.md["guides/agents.md"]
	if bytes.Contains(body, []byte("npx skills add")) || !bytes.Contains(body, []byte("this install")) {
		t.Error(".md hosted/self blocks were not resolved")
	}
	if bytes.Contains(body, []byte("<!--sh:")) {
		t.Error(".md copy still carries an unresolved marker")
	}
	// webhooks.md's sample payload URL is "https://rosas-bakery.rosa.simple-host.app/":
	// the bare host name inside it is outside the baseURL's exact-origin
	// replace, so only selfHostedChromeRewrite's instanceHosts pass catches
	// it. The HTML got that pass already; the .md path once did not
	// (docsRuntimeText fixes that by sharing the same text passes).
	if webhooks := self.docs.md["webhooks.md"]; bytes.Contains(webhooks, []byte("simple-host.app")) {
		t.Error(".md copy leaked a simple-host.app host name on a self-hosted pipeline")
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
	host := testAssetPipeline(edition.Default(config.DefaultLimits()), "")
	host.docs.once.Do(host.buildDocsCache)
	if !bytes.Contains(host.docs.pages["guides/agents/index.html"], []byte("npx skills add")) {
		t.Fatal("hosted instructions absent")
	}
	SetInstanceHosts("docs-box.example", "sites.docs-box.example", "")
	limits := withLimits(t, map[string]string{"MAX_SITES_PER_ACCOUNT": "250", "DOMAIN_UNPROVEN_HOURS": "36"})
	for _, kind := range []*edition.Edition{platformTestEdition()} {
		p := testAssetPipeline(edition.Default(limits), "https://docs-box.example")
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
		if kind.Identity().PersonalSites && strings.Contains(string(p.docs.pages["guides/addresses/index.html"]), "take-a-free-short-name") {
			t.Error("hosted-only address task or TOC link leaked into self-hosted docs")
		}
		limitsBody := string(p.docs.pages["limits/index.html"])
		if kind.Identity().PersonalSites && !strings.Contains(limitsBody, "Up to 250 sites") {
			t.Error("limit wording was not rewritten")
		}
		if !strings.Contains(limitsBody, "Up to 2,345,678 bytes") || !strings.Contains(limitsBody, "Up to 123,456 bytes per file") {
			t.Error("storage byte allowances were not rewritten")
		}
		for _, body := range [][]byte{p.docs.pages["self-hosting/settings/storage-migration/index.html"], p.docs.md["self-hosting/settings/storage-migration.md"]} {
			if bytes.Contains(body, []byte("17 December 2026")) {
				t.Error("hosted switch date shown on a self-hosted install")
			}
		}
		for _, body := range [][]byte{p.docs.pages["guides/moving-saved-data/index.html"], p.docs.md["guides/moving-saved-data.md"]} {
			if bytes.Contains(body, []byte("https://simple-host.app/docs/")) || !bytes.Contains(body, []byte("https://docs-box.example/docs/guides/storage")) {
				t.Error("moving guide did not localize its storage guide link")
			}
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
	p := testAssetPipeline(edition.Default(config.DefaultLimits()), "")
	p.docs.once.Do(p.buildDocsCache)
	fresh := testAssetPipeline(edition.Default(config.DefaultLimits()), "")
	for name, body := range p.docs.scripts {
		r := httptest.NewRecorder()
		fresh.serveDocs(r, httptest.NewRequest("GET", "/docs/"+name, nil))
		if r.Code != 200 || !bytes.Equal(r.Body.Bytes(), body) {
			t.Errorf("cached script %s unavailable after restart", name)
		}
	}
}

func TestDocsSitemap(t *testing.T) {
	for _, product := range []bool{false} {
		r := httptest.NewRecorder()
		base, _ := sitemapBase(httptest.NewRequest("GET", "https://example.org/sitemap.xml", nil), product)
		serveSitemapFile(r, httptest.NewRequest("GET", "https://example.org/sitemap.xml", nil), buildSitemaps(base, sitemapPages(base, product, webassets.DocsTree()), nil))
		for _, route := range docsPaths(webassets.DocsTree()) {
			if !strings.Contains(r.Body.String(), route+"</loc>") {
				t.Errorf("sitemap missing %s", route)
			}
		}
		if product && strings.Contains(r.Body.String(), "/docs/guides/addresses/") {
			t.Error("Hack sitemap includes Host-only guide")
		}
	}
}

func TestDocsOldAnchors(t *testing.T) {
	// Run the actual external script against a tiny location stub, offline.
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable for anchor compatibility test")
	}
	data, err := fs.ReadFile(webassets.DocsTree(), "old-anchors.js")
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
	data, err := fs.ReadFile(webassets.DocsTree(), "old-anchors.js")
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
	for _, product := range []bool{false} {
		tree := webassets.DocsTree()
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
					t.Errorf("hack=%v: install-skills target = %q", product, target)
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
				t.Errorf("hack=%v #%s: %v", product, anchor, err)
				continue
			}
			if u.Fragment != "" && !regexp.MustCompile(`<h[1-6]\b[^>]*\bid="`+regexp.QuoteMeta(u.Fragment)+`"`).Match(page) {
				t.Errorf("hack=%v #%s: missing heading %s", product, anchor, target)
			}
		}
	}
}

func TestDocsDirectoryRedirectQuery(t *testing.T) {
	for _, kind := range []*edition.Edition{platformTestEdition()} {
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
	RegisterUIRoutes(mux, os.Getenv("PUBLIC_BASE_URL"), chromeTestHandler(platformTestEdition()))
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
	for _, product := range []bool{false} {
		tree := webassets.DocsTree()
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
				if !regexp.MustCompile(`<[^>]+\bdata-pagefind-ignore(?:[\s=>])`).Match(block[1]) {
					t.Errorf("%s: instance text indexed", name)
				}
			}
			if product && (bytes.Contains(body, []byte("Sites per account")) || bytes.Contains(body, []byte("Follow Connect Domain"))) {
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
	for _, kind := range []*edition.Edition{platformTestEdition()} {
		body := get(t, chromeTestMux(t, kind), "", "/features.html").Body.String()
		has := strings.Contains(body, `href="/skills/connect-domain/SKILL.md"`)
		if has != (kind.Identity().PersonalSites) {
			t.Errorf("%v: connect-domain link present=%v", kind, has)
		}
	}
}
