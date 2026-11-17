package handler

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"strings"
	"sync"
	"time"
)

const (
	markerSigninMessages = "/*sh:signin-messages*/{}"
	markerSigninCard     = "  /*sh:signin-card*/"
)

// Public copy uses simple-host.app. The old base-domain text rewriter stays
// available for compatibility; no public copy advertises the paused base.

const canonicalBaseDomain = "simple-host.site"

var (
	baseTextMu sync.RWMutex
	// baseTextTo is what canonicalBaseDomain is served as ("" = as written).
	baseTextTo = canonicalSiteDomain
	// authJSBases are the served bases while the base moves (nil otherwise):
	// auth.js then treats a one-label host under any of them as a person
	// host (authJSPatch).
	authJSBases []string
	// teamSiteFlatHostsText: this instance's TEAM_SITE_FLAT_HOSTS is on
	// (set by SiteHandler.SetTeamSiteFlatHosts, hack_sites.go, only when the
	// edition also runs hosted events). auth.js then decides the flat team
	// case first, before the person-path heuristic, taking the part of a
	// flat team-site host's one label before the first "--" as the site
	// name for every path (authJSOwnHostChainFlat).
	teamSiteFlatHostsText bool
)

// authJSOneLabel is auth.js's person-host test: one label under the auth
// apex. Under a moving base a person host is one label under either base, and
// auth.js is one file served from the app to pages on both, so the served
// copy names both (a test fails if this line changes in auth.js). Unpatched,
// auth.js is byte for byte what it was before the move existed.
const authJSOneLabel = `    var oneLabel = host.slice(-(apexHost.length + 1)) === "." + apexHost &&
      host.split(".").length === apexHost.split(".").length + 1 && sub !== "www";`

// authJSPatched is authJSOneLabel for a moving base.
func authJSPatched(bases []string) string {
	j, _ := json.Marshal(bases)
	return `    var oneLabel = [apexHost].concat(` + string(j) + `).some(function (b) {
      return host.slice(-(b.length + 1)) === "." + b && host.split(".").length === b.split(".").length + 1;
    }) && sub !== "www";`
}

// SetSiteBaseText tells the served auth.js about a moving base: bases are
// the domains people's addresses answer under (SiteHandler.ServedBases).
// Fewer than two means none and leaves auth.js as written.
func SetSiteBaseText(bases []string) {
	baseTextMu.Lock()
	if len(bases) > 1 {
		authJSBases = append([]string(nil), bases...)
	} else {
		authJSBases = nil
	}
	baseTextMu.Unlock()
	clearAssetText()
}

// authJSOwnHostChain is auth.js's whole API-base decision chain for a page
// not on a claimed name (_cfg.site unset): sites.<apex>/<handle>/<site>/,
// <handle>.<apex>/<site>/... (the person-path heuristic, oneLabel && seg)
// and the site-host/catch-all else. A test fails if this text changes.
const authJSOwnHostChain = `    if (sub === "sites" && m) {
      API_BASE = location.origin + "/v1/u/" + m[1] + "/sites/" + m[2];
    } else if (oneLabel && seg) {
      // <handle>.<apex>/<site>/...: a person's own address, where the site is
      // the first path segment. A <name>.<apex> a site has claimed serves that
      // site at its root instead. Ask the host once which it is.
      var bySeg = location.origin + "/v1/sites/" + seg[1];
      var byLabel = location.origin + "/v1/sites/" + sub;
      API_BASE = BASE_TOKEN;
      baseReady = fetch(bySeg + "/me", {credentials: "include", cache: "no-store"}).then(
        function (r) { return r.ok ? bySeg : byLabel; },
        function () { return byLabel; });
    } else {
      // <site>.<handle>.<apex> (a site's own host) and every other host:
      // the site is the first label, and /v1/ is same-origin.
      API_BASE = location.origin + "/v1/sites/" + sub;
      ownOrigin = !contentHost;
    }`

// authJSOwnHostChainFlat is authJSOwnHostChain for TEAM_SITE_FLAT_HOSTS: the
// flat team case is decided first, before the person-path heuristic below
// it, so a sub-path request (e.g. alpha--weekend.<host>/about/) still
// resolves to the team's own site instead of trying "site is the first path
// segment". A flat team-site host's one label is <team>--<event>; the site
// is the part before the first "--", for every path. (A pre-existing event
// slug that happens to contain "--", impossible for one created after this
// shipped, still resolves correctly server-side —
// handler/sitebase.go's exact-handle-match-wins siteHostParts — but this
// client-side guess has no database to tell the two apart by shape alone.)
const authJSOwnHostChainFlat = `    if (sub.indexOf("--") !== -1) {
      API_BASE = location.origin + "/v1/sites/" + sub.split("--")[0];
      ownOrigin = !contentHost;
    } else if (sub === "sites" && m) {
      API_BASE = location.origin + "/v1/u/" + m[1] + "/sites/" + m[2];
    } else if (oneLabel && seg) {
      // <handle>.<apex>/<site>/...: a person's own address, where the site is
      // the first path segment. A <name>.<apex> a site has claimed serves that
      // site at its root instead. Ask the host once which it is.
      var bySeg = location.origin + "/v1/sites/" + seg[1];
      var byLabel = location.origin + "/v1/sites/" + sub;
      API_BASE = BASE_TOKEN;
      baseReady = fetch(bySeg + "/me", {credentials: "include", cache: "no-store"}).then(
        function (r) { return r.ok ? bySeg : byLabel; },
        function () { return byLabel; });
    } else {
      // <site>.<handle>.<apex> (a site's own host) and every other host:
      // the site is the first label, and /v1/ is same-origin.
      API_BASE = location.origin + "/v1/sites/" + sub;
      ownOrigin = !contentHost;
    }`

// setTeamSiteFlatHostsText tells the served auth.js that this instance's
// team sites are flat (SiteHandler.SetTeamSiteFlatHosts).
func setTeamSiteFlatHostsText(on bool) {
	baseTextMu.Lock()
	teamSiteFlatHostsText = on
	baseTextMu.Unlock()
	clearAssetText()
}

func isAuthJS(name string) bool {
	return name == "static/auth.js" || name == "auth.js"
}

func isSigninJS(name string) bool {
	return name == "static/signin.js" || name == "signin.js"
}

func injectSigninMessages(b []byte) []byte {
	js, err := json.Marshal(signInMessages)
	if err != nil {
		panic("signInMessages: " + err.Error())
	}
	return bytes.Replace(b, []byte(markerSigninMessages), append([]byte("/*sh:signin-messages*/"), js...), 1)
}

func servedSigninJS() []byte {
	raw, err := embeddedStatic.ReadFile("static/signin.js")
	if err != nil {
		panic("signin.js missing from the embedded static files: " + err.Error())
	}
	return injectSigninMessages(baseText(raw))
}

// transformStatic is what an embedded file is served as: the base swap, the
// sign-in message table in signin.js, the sign-in card spliced into auth.js,
// and for auth.js the moving-base patch (and hackAuthJS for Hack assets). A
// nil pipeline is the product-neutral view: no hackAuthJS.
func (p *assetPipeline) transformStatic(name string, b []byte) []byte {
	b = baseText(b)
	if p != nil && !isAuthJS(name) && p.product.rewrite != nil {
		b = p.product.rewrite(name, b)
	}
	if isSigninJS(name) {
		return injectSigninMessages(b)
	}
	if isAuthJS(name) {
		b = bytes.Replace(b, []byte(markerSigninCard), servedSigninJS(), 1)
		if p != nil && p.pres.ProductAssets {
			if p.product.rewrite != nil {
				b = p.product.rewrite(name, b)
			}
		}
		baseTextMu.RLock()
		bases := authJSBases
		flat := teamSiteFlatHostsText
		baseTextMu.RUnlock()
		if bases != nil {
			b = bytes.Replace(b, []byte(authJSOneLabel), []byte(authJSPatched(bases)), 1)
		}
		if flat {
			b = bytes.Replace(b, []byte(authJSOwnHostChain), []byte(authJSOwnHostChainFlat), 1)
		}
		return b
	}
	return b
}

// staticUnchanged: files are served exactly as embedded.
func (p *assetPipeline) staticUnchanged() bool {
	baseTextMu.RLock()
	defer baseTextMu.RUnlock()
	return baseTextTo == "" && authJSBases == nil && (p == nil || p.product.rewrite == nil)
}

// setBaseText picks what the embedded text says for people's addresses:
// simple-host.site as written only on the hosted service once it hands those
// addresses out, simple-host.app everywhere else.
func setBaseText(siteDomain, handoutBase string) {
	to := canonicalSiteDomain
	if strings.EqualFold(siteDomain, canonicalSiteDomain) && strings.EqualFold(handoutBase, canonicalBaseDomain) {
		to = ""
	}
	baseTextMu.Lock()
	baseTextTo = to
	baseTextMu.Unlock()
	clearAssetText()
	chromeCache.Clear()
}

func currentBaseTextTo() string {
	baseTextMu.RLock()
	defer baseTextMu.RUnlock()
	return baseTextTo
}

// baseText applies the swap to b (b itself when there is nothing to swap).
func baseText(b []byte) []byte {
	to := currentBaseTextTo()
	if to == "" || !bytes.Contains(b, []byte(canonicalBaseDomain)) {
		return b
	}
	return bytes.ReplaceAll(b, []byte(canonicalBaseDomain), []byte(to))
}

// baseTextString is baseText for a string.
func baseTextString(s string) string {
	to := currentBaseTextTo()
	if to == "" || !strings.Contains(s, canonicalBaseDomain) {
		return s
	}
	return strings.ReplaceAll(s, canonicalBaseDomain, to)
}

// baseTextFS serves an embedded tree with baseText applied to every file that
// mentions the base domain. Directories, and files that do not mention it, are
// the embedded ones themselves (same FileInfo, same zero ModTime), so the file
// server's headers are unchanged; a swapped file keeps its FileInfo but for
// its size.
//
// p is the pipeline the tree is served for (assetPipeline.staticFS): its
// presentation picks auth.js's form and its cache holds the swapped files.
// The package's staticFiles has none: it is the product-neutral view, read by
// code that never reads auth.js, and swaps without caching.
type baseTextFS struct {
	fsys interface {
		fs.ReadFileFS
		fs.ReadDirFS
	}
	p *assetPipeline
}

func (f baseTextFS) ReadFile(name string) ([]byte, error) {
	b, err := f.fsys.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return f.p.transformStatic(name, b), nil
}

func (f baseTextFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return f.fsys.ReadDir(name)
}

func (f baseTextFS) Open(name string) (fs.File, error) {
	file, err := f.fsys.Open(name)
	if err != nil {
		return file, err
	}
	// auth.js and signin.js always go through transformStatic, even on the
	// canonical host where every other file is served byte-for-byte.
	if f.p.staticUnchanged() && !isAuthJS(name) && !isSigninJS(name) {
		return file, nil
	}
	st, err := file.Stat()
	if err != nil || st.IsDir() {
		return file, nil
	}
	b, err := f.swapped(name)
	if err != nil || b == nil {
		return file, nil
	}
	_ = file.Close()
	return &baseTextFile{Reader: bytes.NewReader(b), info: sizedInfo{FileInfo: st, size: int64(len(b))}}, nil
}

// swapped is name as served, or nil when that is the embedded file itself.
// The pipeline caches it; the neutral view recomputes it.
func (f baseTextFS) swapped(name string) ([]byte, error) {
	key := currentBaseTextTo() + "\x00" + name
	if f.p != nil {
		if f.p.pres.ProductAssets {
			key += "\x00hack"
		}
		if body, ok := f.p.text.Load(key); ok {
			return body.([]byte), nil
		}
	}
	raw, err := f.fsys.ReadFile(name)
	if err != nil {
		return nil, err
	}
	swapped := f.p.transformStatic(name, raw)
	if len(swapped) == len(raw) && bytes.Equal(swapped, raw) {
		swapped = nil // nothing to swap: serve the embedded file
	}
	if f.p == nil {
		return swapped, nil
	}
	body, _ := f.p.text.LoadOrStore(key, swapped)
	return body.([]byte), nil
}

// baseTextFile is a swapped file: seekable, like an embedded one.
type baseTextFile struct {
	*bytes.Reader
	info fs.FileInfo
}

func (f *baseTextFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *baseTextFile) Close() error               { return nil }

// sizedInfo is a FileInfo with another size.
type sizedInfo struct {
	fs.FileInfo
	size int64
}

func (s sizedInfo) Size() int64        { return s.size }
func (s sizedInfo) ModTime() time.Time { return s.FileInfo.ModTime() }
