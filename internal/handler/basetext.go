package handler

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"strings"
	"sync"
	"time"
)

// canonicalBaseDomain is the base domain people's addresses are written under
// in the embedded text (sitebase.go): every page, llms.txt and the OpenAPI
// spec name a person's or site's address as <...>.simple-host.site, and the
// app itself as simple-host.app.
//
// Until the hosted service hands those addresses out (SITE_BASE_MOVE
// canonical), and on every other install, the text is served with
// simple-host.site swapped back to simple-host.app: byte for byte what it was
// before the move existed, so the host rewriter (instancehost.go) and every
// header the file server sends stay exactly as they were.
const canonicalBaseDomain = "simple-host.site"

var (
	baseTextMu sync.RWMutex
	// baseTextTo is what canonicalBaseDomain is served as ("" = as written).
	baseTextTo = canonicalSiteDomain
	// baseTextCache holds swapped copies of embedded files, by name.
	baseTextCache sync.Map
	// authJSBases are the served bases while the base moves (nil otherwise):
	// auth.js then treats a one-label host under any of them as a person
	// host (authJSPatch).
	authJSBases []string
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
	baseTextCache.Clear()
}

// transformStatic is what an embedded file is served as: the base swap, and
// for auth.js the moving-base patch.
func transformStatic(name string, b []byte) []byte {
	b = baseText(b)
	baseTextMu.RLock()
	bases := authJSBases
	baseTextMu.RUnlock()
	if bases != nil && (name == "static/auth.js" || name == "auth.js") {
		b = bytes.Replace(b, []byte(authJSOneLabel), []byte(authJSPatched(bases)), 1)
	}
	return b
}

// staticUnchanged: files are served exactly as embedded.
func staticUnchanged() bool {
	baseTextMu.RLock()
	defer baseTextMu.RUnlock()
	return baseTextTo == "" && authJSBases == nil
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
	baseTextCache.Clear()
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
type baseTextFS struct {
	fsys interface {
		fs.ReadFileFS
		fs.ReadDirFS
	}
}

func (f baseTextFS) ReadFile(name string) ([]byte, error) {
	b, err := f.fsys.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return transformStatic(name, b), nil
}

func (f baseTextFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return f.fsys.ReadDir(name)
}

func (f baseTextFS) Open(name string) (fs.File, error) {
	file, err := f.fsys.Open(name)
	if err != nil || staticUnchanged() {
		return file, err
	}
	st, err := file.Stat()
	if err != nil || st.IsDir() {
		return file, nil
	}
	key := currentBaseTextTo() + "\x00" + name
	body, ok := baseTextCache.Load(key)
	if !ok {
		raw, err := f.fsys.ReadFile(name)
		if err != nil {
			return file, nil
		}
		swapped := transformStatic(name, raw)
		if len(swapped) == len(raw) && bytes.Equal(swapped, raw) {
			swapped = nil // nothing to swap: serve the embedded file
		}
		body, _ = baseTextCache.LoadOrStore(key, swapped)
	}
	b := body.([]byte)
	if b == nil {
		return file, nil
	}
	_ = file.Close()
	return &baseTextFile{Reader: bytes.NewReader(b), info: sizedInfo{FileInfo: st, size: int64(len(b))}}, nil
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
