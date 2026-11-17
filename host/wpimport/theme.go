package wpimport

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"
)

var pageIDClass = regexp.MustCompile(`\bpage-id-(\d+)\b`)
var cssURL = regexp.MustCompile(`(?i)url\(\s*['"]?([^'"\)]+)['"]?\s*\)`)
var cssImport = regexp.MustCompile(`(?i)@import\s+(?:url\()?\s*['"]?([^'"\s;)]+)['"]?\s*\)?[^;]*;`)

func (c *converter) copyTheme() {
	if c.in.ThemeURL == "" || c.in.Fetch == nil {
		return
	}
	b, err := c.in.Fetch(c.ctx, c.in.ThemeURL)
	if err != nil {
		if !c.squarespace() {
			c.warn("Theme page could not be fetched.")
		}
		return
	}
	c.homepage = string(b)
	doc, _ := xhtml.Parse(strings.NewReader(string(b)))
	walk(doc, func(n *xhtml.Node) {
		if n.Data == "body" {
			c.bodyClasses = getAttr(n, "class")
		}
	})
	// Squarespace stylesheets only work with Squarespace's own page code.
	// The page is still fetched: the menu is read from it, and its body
	// classes still identify the front page.
	if c.squarespace() {
		return
	}
	root := fragment(string(b))
	base, _ := url.Parse(c.in.ThemeURL)
	count := 0
	var total int
	ok := true
	cache := map[string]string{}
	staged := map[string][]byte{}
	allowed := func(u *url.URL, asset bool) bool {
		h := strings.ToLower(u.Hostname())
		return (u.Scheme == "https" || u.Scheme == "http") && (h == base.Hostname() || strings.HasSuffix(h, ".wp.com") || h == "fonts.googleapis.com" || asset && h == "fonts.gstatic.com")
	}
	resolve := func(raw, from string) string {
		b, e := url.Parse(from)
		if e != nil {
			return ""
		}
		u, e := url.Parse(strings.TrimSpace(raw))
		if e != nil {
			return ""
		}
		return b.ResolveReference(u).String()
	}
	var fetchCSS func(string, int) string
	var rewriteCSS func(string, string, int) string
	asset := func(raw, from string) string {
		abs := resolve(raw, from)
		u, e := url.Parse(abs)
		if e != nil || !allowed(u, true) {
			ok = false
			return ""
		}
		if p, yes := cache[abs]; yes {
			return p
		}
		data, e := c.in.Fetch(c.ctx, abs)
		if e != nil {
			ok = false
			return ""
		}
		total += len(data)
		if total > 5<<20 {
			ok = false
			return ""
		}
		sum := sha256.Sum256([]byte(abs))
		ext := path.Ext(u.Path)
		if len(ext) > 10 {
			ext = ""
		}
		p := fmt.Sprintf("sh-import/theme/%x%s", sum[:8], ext)
		cache[abs] = p
		staged[p] = data
		return p
	}
	rewriteCSS = func(css, from string, depth int) string {
		css = cssImport.ReplaceAllStringFunc(css, func(s string) string {
			m := cssImport.FindStringSubmatch(s)
			p := fetchCSS(resolve(m[1], from), depth+1)
			if p == "" {
				return ""
			}
			return `@import url("` + path.Base(p) + `");`
		})
		return cssURL.ReplaceAllStringFunc(css, func(s string) string {
			m := cssURL.FindStringSubmatch(s)
			raw := strings.TrimSpace(m[1])
			if strings.HasPrefix(raw, "data:") || strings.HasPrefix(raw, "#") || (!strings.Contains(raw, "/") && staged["sh-import/theme/"+raw] != nil) {
				return s
			}
			p := asset(raw, from)
			if p == "" {
				return `url("")`
			}
			return `url("` + path.Base(p) + `")`
		})
	}
	fetchCSS = func(abs string, depth int) string {
		if p, yes := cache[abs]; yes {
			return p
		}
		u, e := url.Parse(abs)
		if e != nil || !allowed(u, false) || depth > 5 || count >= 40 {
			ok = false
			return ""
		}
		count++
		data, e := c.in.Fetch(c.ctx, abs)
		if e != nil {
			ok = false
			return ""
		}
		total += len(data)
		if total > 5<<20 {
			ok = false
			return ""
		}
		sum := sha256.Sum256([]byte(abs))
		p := fmt.Sprintf("sh-import/theme/%x.css", sum[:8])
		cache[abs] = p
		staged[p] = []byte(rewriteCSS(string(data), abs, depth))
		return p
	}
	walk(root, func(n *xhtml.Node) {
		if n.Data == "link" && strings.Contains(strings.ToLower(getAttr(n, "rel")), "stylesheet") {
			if p := fetchCSS(resolve(getAttr(n, "href"), c.in.ThemeURL), 0); p != "" {
				c.themeLinks = append(c.themeLinks, p)
			}
		}
		if n.Data == "style" {
			id := getAttr(n, "id")
			for _, marker := range []string{"global-styles", "wp-custom-css", "custom-css", "-inline-css"} {
				if strings.Contains(id, marker) {
					if count >= 40 || total > 5<<20 {
						ok = false
						break
					}
					count++
					var s strings.Builder
					for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
						s.WriteString(ch.Data)
					}
					total += s.Len()
					if total > 5<<20 {
						ok = false
						break
					}
					p := fmt.Sprintf("sh-import/theme/inline-%d.css", len(c.themeLinks))
					staged[p] = []byte(rewriteCSS(s.String(), c.in.ThemeURL, 0))
					c.themeLinks = append(c.themeLinks, p)
					break
				}
			}
		}
	})
	if !ok {
		c.themeLinks = nil
		c.warn("Theme copy was incomplete; the built-in theme is used.")
		return
	}
	for p, b := range staged {
		c.result.Files[p] = b
	}
	c.result.Report.ThemeCopied = len(c.themeLinks) > 0
}

const themeCSS = `:root{color-scheme:light dark;--bg:#fff;--fg:#222;--muted:#666;--line:#ddd;--accent:#195e97}*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:18px/1.7 system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}a{color:var(--accent)}.site-header,.site-main,footer{max-width:78ch;margin:auto;padding:1.5rem}.site-title{font-size:1.5rem;font-weight:700;text-decoration:none}.main-navigation ul{list-style:none;padding:0;display:flex;gap:1rem;flex-wrap:wrap}.main-navigation details ul{display:block;padding-left:1rem}.main-navigation summary{cursor:pointer}.entry-title{line-height:1.2}img,video{max-width:100%;height:auto}figure{margin:1.5rem 0}figcaption,.wp-caption-text,time{font-size:.9rem;color:var(--muted)}.gallery,.wp-block-gallery,.wp-block-columns{display:flex;gap:1rem;flex-wrap:wrap}.gallery>figure,.wp-block-gallery>figure{flex:1 1 180px}.gallery{display:grid;grid-template-columns:repeat(var(--gallery-columns,3),minmax(0,1fr))}.gallery-circle img{border-radius:50%}.gallery-size-thumbnail img{max-height:150px}.gallery-size-medium img{max-height:300px}.gallery img{width:100%;object-fit:cover}.wp-block-column{flex:1 1 220px}.wp-block-group{padding:1rem}.wp-block-buttons{display:flex;gap:1rem}.wp-block-button__link,button{border:0;border-radius:.3rem;padding:.65rem 1rem;background:var(--accent);color:white;cursor:pointer}.wp-block-cover{padding:2rem;position:relative}.wp-block-cover__image-background{position:absolute;inset:0;width:100%;height:100%;object-fit:cover}.wp-block-cover__inner-container{position:relative}.wp-block-media-text{display:grid;grid-template-columns:1fr 1fr;gap:1rem}.wp-block-separator,hr{border:0;border-top:1px solid var(--line)}blockquote,.wp-block-pullquote{border-left:3px solid var(--accent);padding-left:1.2rem}table{border-collapse:collapse;width:100%}td,th{border:1px solid var(--line);padding:.5rem}.wp-block-table{overflow:auto}.aligncenter{text-align:center;margin-left:auto;margin-right:auto}.alignwide{max-width:100%}.alignfull{width:100%}.embed{aspect-ratio:16/9}.embed iframe{width:100%;height:100%;border:0}.embed-map{aspect-ratio:4/3}.wp-block-jetpack-slideshow,.wp-block-jetpack-slideshow .swiper-wrapper{display:flex;overflow:auto;scroll-snap-type:x mandatory;gap:1rem}.wp-block-jetpack-slideshow figure,.wp-block-jetpack-slideshow .swiper-slide{flex:0 0 90%;scroll-snap-align:start}.wp-block-jetpack-tiled-gallery{display:flex;flex-wrap:wrap;gap:.5rem}.wp-block-jetpack-slideshow img{width:100%;max-height:70vh;object-fit:contain}.wp-block-jetpack-slideshow [role=button],.wp-block-jetpack-slideshow .swiper-pagination{display:none}.business-hours{display:grid;grid-template-columns:max-content 1fr;gap:.25rem 1.5rem}.business-hours dd{margin:0}.sh-widget{border-top:1px solid var(--line);margin-top:2rem;padding-top:1rem}.sh-widget label{display:block;margin:.7rem 0}.sh-widget input:not([type=checkbox]):not([type=radio]),.sh-widget textarea,.sh-widget select{display:block;width:100%;font:inherit;padding:.5rem;border:1px solid var(--line);border-radius:.25rem;background:var(--bg);color:var(--fg)}.sh-widget [role=status]{min-height:1.7em}footer{border-top:1px solid var(--line);font-size:.85rem}pre{overflow:auto}nav[aria-label="Archive pages"] a{padding:.5rem}.comment .children{margin-left:1.5rem;border-left:2px solid var(--line);padding-left:1rem}@media(max-width:600px){body{font-size:16px}.site-header,.site-main,footer{padding:1rem}.wp-block-media-text{display:block}.gallery{grid-template-columns:repeat(min(var(--gallery-columns,3),2),minmax(0,1fr))}.main-navigation ul{gap:.7rem}.comment .children{margin-left:.75rem;padding-left:.5rem}}.site-header{display:flex;align-items:center;gap:1.5rem;flex-wrap:wrap}.main-navigation{flex:1 1 100%}.header-search{margin-left:auto}.nav-mobile{display:none}.main-navigation .nav-wide>ul,.wp-block-navigation .nav-wide>ul{display:flex;flex-wrap:wrap;gap:1rem;list-style:none;padding:0}.wp-block-navigation ul{list-style:none;padding:0}.wp-block-navigation summary{cursor:pointer}img{max-width:100%!important;height:auto!important}figure{max-width:100%;min-width:0}figure img{display:block}.gallery,.wp-block-gallery,.blocks-gallery-grid{display:grid;grid-template-columns:repeat(var(--gallery-columns,3),minmax(0,1fr));gap:1rem;list-style:none;padding:0;max-width:100%}.wp-block-gallery ul{list-style:none;padding:0;margin:0}.wp-block-gallery>.blocks-gallery-grid{grid-column:1/-1;width:100%;grid-template-columns:inherit}.gallery>figure,.wp-block-gallery>figure,.blocks-gallery-grid>li,.blocks-gallery-item{width:auto!important;min-width:0;margin:0!important}.gallery img,.wp-block-gallery img{max-width:100%!important;height:auto!important}.sh-widget [data-sh-topics]{display:flex;flex-direction:column;gap:.5rem}.sh-widget [data-sh-topics] button{text-align:left}.sh-widget [data-sh-thread] article{border-bottom:1px solid var(--line);padding:1rem 0}.sh-widget [data-sh-thread] time{display:block}.columns-1,.gallery-columns-1{--gallery-columns:1}.columns-2,.gallery-columns-2{--gallery-columns:2}.columns-3,.gallery-columns-3{--gallery-columns:3}.columns-4,.gallery-columns-4{--gallery-columns:4}.columns-5,.gallery-columns-5{--gallery-columns:5}.columns-6,.gallery-columns-6{--gallery-columns:6}.columns-7,.gallery-columns-7{--gallery-columns:7}.columns-8,.gallery-columns-8{--gallery-columns:8}.columns-9,.gallery-columns-9{--gallery-columns:9}@media(min-width:700px){.site-header .site-title{flex:0 1 auto}.main-navigation{flex:1 1 350px}}@media(max-width:699px){.nav-wide,.header-search{display:none}.nav-mobile{display:block}.nav-mobile summary{cursor:pointer}.nav-mobile ul,.main-navigation details ul{display:flex;flex-direction:column;gap:.6rem;padding:0;list-style:none}.main-navigation{flex:1 1 auto}.site-header{align-items:baseline;gap:1rem}.site-title{flex:1}.main-navigation .nav-mobile>summary{text-align:right}.gallery,.wp-block-gallery,.blocks-gallery-grid{grid-template-columns:repeat(auto-fill,minmax(140px,1fr))}.wp-block-gallery>.blocks-gallery-grid{grid-template-columns:repeat(auto-fill,minmax(140px,1fr))}.wp-block-gallery ul{width:100%}}.wp-block-jetpack-slideshow ul,.wp-block-jetpack-slideshow li,.wp-block-jetpack-tiled-gallery ul,.wp-block-jetpack-tiled-gallery li{list-style:none;padding-left:0}.wp-block-jetpack-slideshow ul{margin:0}.wp-block-jetpack-slideshow li{min-width:0}@media(max-width:699px){.main-navigation{flex:0 0 auto}.main-navigation .nav-mobile{position:relative;width:max-content}.main-navigation .nav-mobile>ul{position:absolute;right:0;top:100%;width:min(20rem,calc(100vw - 2rem));padding:1rem;background:var(--bg);border:1px solid var(--line);z-index:10}.site-title{min-width:0}}@media(prefers-color-scheme:dark){:root{--bg:#17191b;--fg:#eee;--muted:#aaa;--line:#444;--accent:#83bff1}button,.wp-block-button__link{color:#17191b}}`
