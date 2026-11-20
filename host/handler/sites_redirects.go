package handler

import (
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/host/sites/pagepath"
	"github.com/vsriram/simple-host/host/sites/redirects"
)

type redirectCacheEntry struct {
	info     os.FileInfo
	size     int64
	modified time.Time
	max      int
	rules    *redirects.Rules
}

var siteRedirectCache = struct {
	sync.Mutex
	entries map[string]redirectCacheEntry
}{entries: make(map[string]redirectCacheEntry)}

func loadRedirects(root *os.Root, max int) *redirects.Rules {
	marker := "redirects"
	st, err := root.Stat(marker)
	if os.IsNotExist(err) {
		marker = "current/_redirects"
		st, err = root.Stat(marker)
	}
	if err != nil {
		return nil
	}
	siteRedirectCache.Lock()
	defer siteRedirectCache.Unlock()
	cacheKey := root.Name() + "/" + marker
	if e, ok := siteRedirectCache.entries[cacheKey]; ok && os.SameFile(e.info, st) && e.size == st.Size() && e.modified.Equal(st.ModTime()) && e.max == max {
		return e.rules
	}
	var rules *redirects.Rules
	if st.Size() <= redirects.MaxFileBytes {
		if data, e := root.ReadFile(marker); e == nil {
			// Validated on publish (loops included); serving only needs the rules.
			rules, err = redirects.ParseWith(data, redirects.Options{MaxRules: max, SkipLoopCheck: true})
		}
	} else {
		_, err = redirects.Parse(make([]byte, redirects.MaxFileBytes+1), max)
	}
	if err != nil {
		log.Printf("site redirects %s: %v", marker, err)
	}
	if len(siteRedirectCache.entries) >= 1024 {
		clear(siteRedirectCache.entries)
	}
	siteRedirectCache.entries[cacheKey] = redirectCacheEntry{st, st.Size(), st.ModTime(), max, rules}
	return rules
}

// rootContentExists: something of the site is already at rel, so a rule
// that is not forced stands aside (pagepath.Taken).
func rootContentExists(root *os.Root, rel string) bool {
	return pagepath.Taken(root, rel)
}

func (h *SiteHandler) applyRedirect(w http.ResponseWriter, r *http.Request, userID, siteName, rel, publicPath string) bool {
	root, err := os.OpenRoot(h.disk.SiteDir(userID, siteName))
	if err != nil {
		return false
	}
	defer root.Close()
	return h.applyRootRedirect(w, r, root, rel, publicPath)
}

func (h *SiteHandler) applyRootRedirect(w http.ResponseWriter, r *http.Request, siteRoot *os.Root, rel, publicPath string) bool {
	rules := loadRedirects(siteRoot, h.limits.RedirectsMaxRules)
	if rules == nil {
		return false
	}
	reqPath := "/" + strings.TrimLeft(rel, "/")
	m, ok := rules.Match(reqPath, r.URL.Query())
	if !ok {
		return false
	}
	root, _ := siteRoot.OpenRoot("current")
	if root != nil {
		defer root.Close()
	}
	if !m.Force && root != nil && rootContentExists(root, rel) {
		return false
	}
	// The site's public mount on this address ("" at a host root,
	// /<handle>/<site> on a person path), compared decoded so an equivalent
	// encoding (/%6Fld for /old) keeps it.
	prefix := ""
	if decoded, err := url.PathUnescape(publicPath); err == nil {
		if p, ok := strings.CutSuffix(strings.TrimRight(decoded, "/"), strings.TrimRight(reqPath, "/")); ok {
			prefix = (&url.URL{Path: strings.TrimRight(p, "/")}).EscapedPath()
		}
	}
	// Rule responses (redirects and 404/410 pages) are revalidated.
	revalidate := func() { revalidatePage(w) }
	location := m.Location(prefix, r.URL.RawQuery)
	if m.Status == 301 || m.Status == 302 {
		u, err := url.Parse(location)
		if err != nil {
			return false
		}
		// Never redirect a request to itself (a rule naming this very
		// address, relative or absolute).
		samePlace := !u.IsAbs() || strings.EqualFold(u.Host, r.Host)
		if samePlace && path.Clean("/"+u.Path) == path.Clean(prefix+reqPath) && u.Query().Encode() == r.URL.Query().Encode() {
			return false
		}
		revalidate()
		w.Header().Set("Location", location)
		w.WriteHeader(m.Status)
		return true
	}
	if root == nil {
		return false
	}
	// A rewrite serves the page its target addresses, by the one page rule.
	u, _ := url.Parse(m.Location("", ""))
	page := pagepath.Resolve(root, u.Path)
	for _, candidate := range []string{page.File, "404.html"} {
		if candidate == "" {
			continue
		}
		f, err := root.Open(candidate)
		if err != nil {
			continue
		}
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() {
			f.Close()
			continue
		}
		ct := mime.TypeByExtension(filepath.Ext(candidate))
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		revalidate()
		w.WriteHeader(m.Status)
		if r.Method != http.MethodHead {
			_, _ = io.Copy(w, f)
		}
		f.Close()
		return true
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	revalidate()
	w.WriteHeader(m.Status)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, http.StatusText(m.Status)+"\n")
	}
	return true
}
