package wpimport

import (
	"bytes"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	xhtml "golang.org/x/net/html"
)

var sizeVariant = regexp.MustCompile(`-\d+x\d+(\.[a-zA-Z0-9]+)$`)

func (c *converter) ownHost(h string) bool {
	h = strings.ToLower(h)
	return c.hosts[h] || strings.HasSuffix(h, ".files.wordpress.com") || h == "i0.wp.com" || h == "i1.wp.com" || h == "i2.wp.com" || h == "i3.wp.com"
}
func (c *converter) mediaPath(raw string) string {
	u, e := url.Parse(raw)
	if e != nil {
		return ""
	}
	if u.Host != "" && !c.ownHost(u.Hostname()) {
		return ""
	}
	p := "/" + strings.TrimLeft(u.Path, "/")
	if strings.HasPrefix(u.Hostname(), "i") && strings.HasSuffix(u.Hostname(), ".wp.com") {
		parts := strings.SplitN(strings.TrimPrefix(p, "/"), "/", 2)
		if len(parts) < 2 {
			return ""
		}
		if !c.ownHost(parts[0]) {
			return "wp-content/uploads/external/" + parts[0] + "/" + strings.TrimPrefix(cleanPath(sizeVariant.ReplaceAllString(parts[1], "$1")), "/")
		}
		if strings.HasSuffix(parts[0], ".files.wordpress.com") {
			return "wp-content/uploads/" + strings.TrimPrefix(cleanPath(sizeVariant.ReplaceAllString(parts[1], "$1")), "/")
		}
		p = "/" + parts[1]
	}
	p = sizeVariant.ReplaceAllString(p, "$1")
	if n := strings.Index(p, "/wp-content/uploads/"); n >= 0 {
		return strings.TrimPrefix(cleanPath(p[n:]), "/")
	}
	if strings.HasSuffix(u.Hostname(), ".files.wordpress.com") {
		return "wp-content/uploads/" + strings.TrimPrefix(cleanPath(p), "/")
	}
	if u.Host == "" {
		p = normalizeArchivePath(strings.TrimPrefix(p, "/"))
		if strings.HasPrefix(p, "wp-content/uploads/") {
			return p
		}
		if k := archiveMatch(p, c.in.Media); k != "" {
			return "wp-content/uploads/" + normalizeArchivePath(k)
		}
		if uploadDate.MatchString(p) {
			return "wp-content/uploads/" + p
		}
	}
	return ""
}
func archiveMatch(target string, files map[string]MediaFile) string {
	best := ""
	score := 0
	ambiguous := false
	target = strings.TrimPrefix(target, "/")
	for _, k := range sortedKeys(files) {
		a := strings.Split(k, "/")
		b := strings.Split(target, "/")
		n := 0
		for x, y := len(a)-1, len(b)-1; x >= 0 && y >= 0 && a[x] == b[y]; x, y = x-1, y-1 {
			n += len(a[x]) + 1
		}
		if n > score {
			score = n
			best = k
			ambiguous = false
		} else if n == score && n > 0 {
			ambiguous = true
		}
	}
	if ambiguous {
		return ""
	}
	return best
}
func (c *converter) copyMedia() {
	refs := c.usedMedia()

	type job struct{ path, url, key string }
	byPath := map[string]*job{}
	for _, raw := range sortedKeys(refs) {
		p := refs[raw]
		c.media[raw] = p
		if byPath[p] == nil {
			j := &job{path: p, url: raw, key: archiveMatch(p, c.in.Media)}
			if strings.Contains(p, "/external/") {
				j.key = ""
			}
			byPath[p] = j
		} else {
			byPath[p].url = raw
		}
		c.mediaOriginal[p] = raw
	}
	for _, i := range c.data.Items {
		if p := c.media[i.Attachment]; p != "" && byPath[p] != nil {
			byPath[p].url = i.Attachment
		}
	}
	n := min(c.limits.FetchConcurrency, 16)
	failures := map[string]bool{}
	budgetShrunk := false
	paths := sortedKeys(byPath)
	fetch := func(j *job) []byte {
		if c.ctx.Err() != nil {
			return nil
		}
		var b []byte
		if j.key != "" {
			m := c.in.Media[j.key]
			if m.Open != nil && m.Size <= c.limits.MaxMediaFileSize {
				if r, err := m.Open(); err == nil {
					var readErr error
					b, readErr = io.ReadAll(io.LimitReader(r, c.limits.MaxMediaFileSize+1))
					_ = r.Close()
					if readErr != nil {
						b = nil
					}
				}
			}
		}
		if len(b) == 0 && c.in.Fetch != nil {
			var err error
			b, err = c.in.Fetch(c.ctx, j.url)
			if err != nil {
				b = nil
			}
		}
		if int64(len(b)) > c.limits.MaxMediaFileSize {
			return nil
		}
		return b
	}
	// Small batches bound downloaded bytes and make budget decisions deterministic.
	for start := 0; start < len(paths); start += n {
		end := min(start+n, len(paths))
		downloaded := make([][]byte, end-start)
		var wg sync.WaitGroup
		for x := start; x < end; x++ {
			wg.Add(1)
			go func(x int) { defer wg.Done(); downloaded[x-start] = fetch(byPath[paths[x]]) }(x)
		}
		wg.Wait()
		for x := start; x < end; x++ {
			p := paths[x]
			b := downloaded[x-start]
			downloaded[x-start] = nil
			if len(b) == 0 {
				failures[p] = true
				continue
			}
			l := c.limits
			if budgetShrunk {
				l.ImageMaxEdge = min(l.ImageMaxEdge, 1200)
				l.JPEGQuality = min(l.JPEGQuality, 75)
			}
			b, note := shrinkImage(b, l)
			if note != "" {
				c.warn(p + ": " + note)
			}
			if b == nil {
				failures[p] = true
				continue
			}
			c.result.Files[p] = b
			if c.mediaBytes() > c.limits.MaxSiteBytes {
				if !budgetShrunk {
					budgetShrunk = true
					l.ImageMaxEdge = min(l.ImageMaxEdge, 1200)
					l.JPEGQuality = min(l.JPEGQuality, 75)
					for _, p := range c.mediaFilesByPriority() {
						small, _ := shrinkImage(c.result.Files[p], l)
						c.result.Files[p] = small
					}
					c.warn("Media was shrunk further to fit the site size limit.")
				}
				for _, p := range c.mediaFilesByPriority() {
					if c.mediaBytes() <= c.limits.MaxSiteBytes {
						break
					}
					delete(c.result.Files, p)
					failures[p] = true
					c.warn("Skipped " + p + " to fit the site size limit.")
				}
			}
		}
		if c.in.Progress != nil {
			c.in.Progress("Shrinking photos", end, len(paths))
		}
	}

	for _, raw := range sortedKeys(refs) {
		p := refs[raw]
		if failures[p] {
			delete(c.media, raw)
			c.result.Report.MediaMissing = append(c.result.Report.MediaMissing, raw)
		}
	}
	c.result.Report.MediaCopied = len(byPath) - len(failures)
}
func shrinkImage(b []byte, l Limits) ([]byte, string) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || format == "gif" {
		return b, ""
	}
	pixels := int64(cfg.Width) * int64(cfg.Height)
	if pixels > l.MaxImagePixels {
		if len(b) < 1<<20 {
			return b, "Photo exceeds the pixel limit and was kept without decoding."
		}
		return nil, "Photo exceeds the pixel limit and was skipped."
	}
	if cfg.Width <= l.ImageMaxEdge && cfg.Height <= l.ImageMaxEdge && len(b) < 64<<10 {
		return b, ""
	}
	src, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return b, "Photo could not be decoded; original kept."
	}
	w, h := cfg.Width, cfg.Height
	if w > l.ImageMaxEdge || h > l.ImageMaxEdge {
		if w >= h {
			h = max(1, h*l.ImageMaxEdge/w)
			w = l.ImageMaxEdge
		} else {
			w = max(1, w*l.ImageMaxEdge/h)
			h = l.ImageMaxEdge
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	var out bytes.Buffer
	if format == "png" || !dst.Opaque() {
		_ = png.Encode(&out, dst)
	} else {
		_ = jpeg.Encode(&out, dst, &jpeg.Options{Quality: l.JPEGQuality})
	}
	note := ""
	if format == "jpeg" && bytes.Contains(b, []byte("Exif\x00\x00")) {
		note = "JPEG EXIF orientation was not applied."
	}
	if out.Len() >= len(b) {
		return b, note
	}
	return out.Bytes(), note
}
func (c *converter) rewriteURL(raw, page string) string {
	if strings.HasPrefix(raw, "#") || raw == "" {
		return raw
	}
	u, e := url.Parse(raw)
	if e != nil {
		return raw
	}
	if p, ok := c.media[raw]; ok {
		return relative(page, p)
	}
	if p := c.mediaPath(raw); p != "" {
		if _, ok := c.result.Files[p]; ok {
			return relative(page, p)
		}
	}
	if u.Host == "" && u.Scheme == "" {
		if strings.HasPrefix(raw, "/") {
			return relative(page, u.Path) + queryFragment(u)
		}
		return raw
	}
	if c.hosts[strings.ToLower(u.Hostname())] {
		p := cleanPath(u.Path)
		for _, key := range []string{"p", "page_id"} {
			if id := u.Query().Get(key); id != "" {
				p = "/p/" + id + "/"
				u.RawQuery = ""
			}
		}
		if p == "/" || c.pageExists(p) {
			return relative(page, p) + queryFragment(u)
		}
	}
	return raw
}
func queryFragment(u *url.URL) string {
	s := ""
	if u.RawQuery != "" {
		s = "?" + u.RawQuery
	}
	if u.Fragment != "" {
		s += "#" + u.EscapedFragment()
	}
	return s
}
func (c *converter) pageExists(p string) bool {
	for _, i := range c.published {
		if i.Path == p {
			return true
		}
	}
	return strings.HasPrefix(p, "/category/") || strings.HasPrefix(p, "/tag/") || strings.HasPrefix(p, "/product-category/") || p == "/search/" || p == "/blog/"
}
func (c *converter) fitBudget() {
	total := func() int64 {
		var n int64
		for _, b := range c.result.Files {
			n += int64(len(b))
		}
		return n
	}
	if total() > c.limits.MaxSiteBytes {
		l := c.limits
		l.ImageMaxEdge = min(l.ImageMaxEdge, 1200)
		l.JPEGQuality = min(l.JPEGQuality, 75)
		for _, p := range c.mediaFilesByPriority() {
			if b, ok := c.result.Files[p]; ok {
				b, _ = shrinkImage(b, l)
				c.result.Files[p] = b
			}
		}
		c.warn("Media was shrunk further to fit the site size limit.")
	}
	files := c.mediaFilesByPriority()
	for _, p := range files {
		if total() <= c.limits.MaxSiteBytes {
			break
		}
		delete(c.result.Files, p)
		c.result.Report.MediaCopied--
		raw := c.mediaOriginal[p]
		if raw != "" {
			c.result.Report.MediaMissing = append(c.result.Report.MediaMissing, raw)
			for _, page := range sortedKeys(c.result.Files) {
				if strings.HasSuffix(page, ".html") {
					c.result.Files[page] = []byte(strings.ReplaceAll(string(c.result.Files[page]), esc(relative("/"+page, p)), esc(raw)))
				}
			}
		}
		c.warn("Skipped " + p + " to fit the site size limit.")
	}
	if total() > c.limits.MaxSiteBytes {
		ks := sortedKeys(c.result.Files)
		sort.SliceStable(ks, func(a, b int) bool { return len(c.result.Files[ks[a]]) > len(c.result.Files[ks[b]]) })
		for _, p := range ks {
			if total() <= c.limits.MaxSiteBytes {
				break
			}
			delete(c.result.Files, p)
			c.warn("Skipped " + p + " because the text and theme exceed the site size limit.")
		}
	}
	c.result.Report.TotalBytes = total()
	sort.Strings(c.result.Report.Warnings)
	sort.Strings(c.result.Report.MediaMissing)
}

func (c *converter) mediaBytes() int64 {
	var total int64
	for p, b := range c.result.Files {
		if strings.HasPrefix(p, "wp-content/uploads/") {
			total += int64(len(b))
		}
	}
	return total
}
func (c *converter) mediaFilesByPriority() []string {
	var files []string
	for p := range c.result.Files {
		if strings.HasPrefix(p, "wp-content/uploads/") {
			files = append(files, p)
		}
	}
	sort.Slice(files, func(a, b int) bool {
		_, fa, ea := image.DecodeConfig(bytes.NewReader(c.result.Files[files[a]]))
		_, fb, eb := image.DecodeConfig(bytes.NewReader(c.result.Files[files[b]]))
		ia := ea == nil && fa != ""
		ib := eb == nil && fb != ""
		if ia != ib {
			return !ia
		}
		na, nb := len(c.result.Files[files[a]]), len(c.result.Files[files[b]])
		if na == nb {
			return files[a] < files[b]
		}
		return na > nb
	})
	return files
}

var archiveYear = regexp.MustCompile(`^[0-9]{4}/[0-9]{2}/`)
var uploadDate = regexp.MustCompile(`^(?:[^/]+/)?[0-9]{4}/[0-9]{2}/`)

func normalizeArchivePath(p string) string {
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	for _, prefix := range []string{"wp-content/uploads/", "uploads/"} {
		if strings.HasPrefix(p, prefix) {
			return strings.TrimPrefix(p, prefix)
		}
	}
	if uploadDate.MatchString(p) {
		parts := strings.Split(p, "/")
		if len(parts) > 3 && !archiveYear.MatchString(p) {
			return strings.Join(parts[1:], "/")
		}
	}
	return p
}

func (c *converter) usedMedia() map[string]string {
	refs := map[string]string{}
	attachments := map[string]*item{}
	canonical := map[string]string{}
	for _, i := range c.data.Items {
		if i.Attachment == "" {
			continue
		}
		p := c.mediaPath(i.Attachment)
		if p == "" {
			continue
		}
		attachments[p] = i
		canonical[p] = p
		if strings.HasSuffix(strings.TrimSuffix(p, path.Ext(p)), "-scaled") {
			base := strings.TrimSuffix(strings.TrimSuffix(p, path.Ext(p)), "-scaled") + path.Ext(p)
			canonical[base] = p
			attachments[base] = i
		}
	}
	used := map[*item]bool{}
	add := func(raw string) {
		p := c.mediaPath(raw)
		if p == "" {
			return
		}
		if v := canonical[p]; v != "" {
			p = v
		}
		refs[raw] = p
		if i := attachments[p]; i != nil {
			used[i] = true
			refs[i.Attachment] = p
		}
	}
	var useID func(string)
	useID = func(id string) {
		if i := c.byID[strings.TrimSpace(id)]; i != nil {
			add(i.Attachment)
		}
	}
	seen := map[*item]bool{}
	var scan func(*item)
	scan = func(i *item) {
		if seen[i] {
			return
		}
		seen[i] = true
		useID(i.metadata("_thumbnail_id"))
		if i.Type == "nav_menu_item" {
			add(i.metadata("_menu_item_url"))
			useID(i.metadata("_menu_item_object_id"))
		}
		var blocks func([]*block)
		blocks = func(bs []*block) {
			for _, b := range bs {
				for _, k := range []string{"id", "mediaId"} {
					useID(attrString(b.Attrs, k))
				}
				if ids, ok := b.Attrs["ids"].([]any); ok {
					for _, id := range ids {
						useID(attrString(map[string]any{"id": id}, "id"))
					}
				}
				if ref := c.byID[attrString(b.Attrs, "ref")]; ref != nil {
					scan(ref)
				}
				for _, k := range []string{"url", "src"} {
					add(attrString(b.Attrs, k))
				}
				blocks(b.Children)
			}
		}
		blocks(parseBlocks(i.Content))
		var shorts func(string)
		shorts = func(s string) {
			renderShortcodes(s, func(sc shortcode) string {
				if sc.Tag == "gallery" {
					for _, id := range strings.Split(attrString(sc.Attrs, "ids"), ",") {
						useID(id)
					}
				}
				for _, k := range []string{"src", "mp3", "ogg", "wav", "mp4", "webm", "poster"} {
					add(attrString(sc.Attrs, k))
				}
				shorts(sc.Body)
				return ""
			})
		}
		shorts(i.Content)
		walk(fragment(i.Content), func(n *xhtml.Node) {
			if n.Data == "style" {
				for _, m := range cssURL.FindAllStringSubmatch(serialize(n), -1) {
					add(m[1])
				}
			}
			for _, a := range n.Attr {
				switch a.Key {
				case "src", "href", "poster":
					add(a.Val)
				case "style":
					for _, m := range cssURL.FindAllStringSubmatch(a.Val, -1) {
						add(m[1])
					}
				}
			}
		})
	}
	for _, i := range c.data.Items {
		if i.Status == "publish" && isPageType(i.Type) || i.Type == "wp_template_part" || i.Type == "wp_navigation" && i.ID == c.headerRef {
			scan(i)
		}
	}
	var menus func([]*menuItem)
	menus = func(ms []*menuItem) {
		for _, m := range ms {
			add(m.URL)
			menus(m.Children)
		}
	}
	menus(c.headerMenu)
	for _, i := range c.data.Items {
		if i.Attachment != "" && !used[i] {
			c.result.Report.MediaUnused++
		}
	}
	return refs
}
