package wpimport

import (
	"context"
	"encoding/json"
	"fmt"
	xhtml "golang.org/x/net/html"
	"net/url"
	"path"
	"sort"
	"strings"
)

type converter struct {
	ctx               context.Context
	in                Input
	limits            Limits
	data              *export
	result            *Result
	byID              map[string]*item
	published         []*item
	posts             []*item
	terms             map[string]term
	hosts             map[string]bool
	media             map[string]string
	mediaOriginal     map[string]string
	features          map[string]*Feature
	themeLinks        []string
	bodyClasses       string
	homepage          string
	postsPath         string
	headerMenu        []*menuItem
	headerRef         string
	footer            string
	externalImages    map[string]bool
	perLangMenu       map[string][]*menuItem
	languages         map[string]bool
	multiLang         bool
	defaultLang       string
	frontTranslations map[string]*item
	pageLang          map[string]string
}

func limitsWithDefaults(l Limits) Limits {
	d := DefaultLimits
	if l.MaxXMLBytes > 0 {
		d.MaxXMLBytes = l.MaxXMLBytes
	}
	if l.MaxItems > 0 {
		d.MaxItems = l.MaxItems
	}
	if l.MaxSiteBytes > 0 {
		d.MaxSiteBytes = l.MaxSiteBytes
	}
	if l.MaxImagePixels > 0 {
		d.MaxImagePixels = l.MaxImagePixels
	}
	if l.ImageMaxEdge > 0 {
		d.ImageMaxEdge = l.ImageMaxEdge
	}
	if l.JPEGQuality > 0 {
		d.JPEGQuality = l.JPEGQuality
	}
	if d.JPEGQuality > 100 {
		d.JPEGQuality = 100
	}
	if l.MaxMediaFileSize > 0 {
		d.MaxMediaFileSize = l.MaxMediaFileSize
	}
	if l.FetchConcurrency > 0 {
		d.FetchConcurrency = l.FetchConcurrency
	}
	return d
}
func convert(ctx context.Context, in Input) (*Result, error) {
	l := limitsWithDefaults(in.Limits)
	if in.Progress != nil {
		in.Progress("Reading your export", 0, len(in.WXR))
	}
	d, w, err := parseWXR(ctx, in.WXR, l)
	if err != nil {
		return nil, err
	}
	c := &converter{ctx: ctx, in: in, limits: l, data: d, result: &Result{Files: map[string][]byte{}, Report: Report{SiteTitle: d.Title, SourceURL: d.Base, Dropped: map[string]int{}, Warnings: w}}, byID: map[string]*item{}, terms: map[string]term{}, hosts: map[string]bool{}, media: map[string]string{}, mediaOriginal: map[string]string{}, features: map[string]*Feature{}}
	for _, s := range []string{d.Base, d.Blog, d.Link, in.ThemeURL} {
		if u, e := url.Parse(s); e == nil && u.Hostname() != "" {
			h := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
			c.hosts[h] = true
			c.hosts["www."+h] = true
		}
	}
	for _, t := range d.Terms {
		c.terms[t.Taxonomy+":"+t.Slug] = t
	}
	for _, i := range d.Items {
		if _, ok := c.byID[i.ID]; !ok || i.ID == "" {
			c.byID[i.ID] = i
		}
	}
	protected := 0
	for _, i := range d.Items {
		if i.ID != "" && c.byID[i.ID] != i {
			continue
		}
		if i.Status == "publish" && !i.public() && isPageType(i.Type) {
			protected++
		}
		if i.public() && isPageType(i.Type) {
			i.Path = c.permalink(i, map[string]bool{})
			c.published = append(c.published, i)
			if i.Type == "post" {
				c.posts = append(c.posts, i)
				c.result.Report.Posts++
			} else {
				c.result.Report.Pages++
			}
		}
		for _, t := range i.Categories {
			if t.Domain == "category" || t.Domain == "post_tag" || t.Domain == "product_cat" {
				k := t.Domain + ":" + t.Slug
				if _, ok := c.terms[k]; !ok {
					c.terms[k] = term{Taxonomy: t.Domain, Slug: t.Slug, Name: t.Name}
				}
			}
		}
	}
	if protected > 0 {
		c.warn(fmt.Sprintf("Left out %d password-protected pages or posts; they stay private and are not on the new site.", protected))
	}
	if len(c.published) == 0 {
		return nil, fmt.Errorf("export has no published pages or posts")
	}
	sort.SliceStable(c.posts, func(a, b int) bool {
		if c.posts[a].Date == c.posts[b].Date {
			return c.posts[a].ID > c.posts[b].ID
		}
		return c.posts[a].Date > c.posts[b].Date
	})
	c.languages = map[string]bool{}
	for _, i := range c.published {
		if l := i.language(); l != "" {
			c.languages[l] = true
		}
	}
	c.multiLang = len(c.languages) >= 2
	c.copyTheme()
	menu := c.buildMenu()
	c.headerMenu = menu
	front, blog := c.frontPage()
	c.postsPath = blog
	c.defaultLang = "en"
	if c.multiLang && front != nil {
		if l := front.language(); l != "" {
			c.defaultLang = l
		}
	}
	c.frontTranslations = c.translationGroup(front)
	c.copyMedia()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.templateParts()
	c.result.Report.MenuItems = menuCount(menu)
	c.result.Files["sh-import/theme.css"] = []byte(themeCSS)
	c.feature(FeatureSearch, "WordPress search", "/search/", 1)
	for _, i := range c.data.Items {
		if i.Type == "jp_pay_product" {
			c.feature(FeatureShop, "Jetpack payments", "/", 0)
		}
	}
	authors, multiAuthor := c.authorInfo()
	for n, i := range c.published {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if in.Progress != nil {
			in.Progress("Building pages", n, len(c.published))
		}
		body := c.renderContent(i.Content, i.Path, 0)
		if i.Type == "product" {
			body = c.product(i, i.Path) + body
		}
		if thumb := c.byID[i.metadata("_thumbnail_id")]; thumb != nil {
			if !c.contentHasImage(body, thumb.Attachment) {
				body = `<figure class="featured-image"><img src="` + esc(thumb.Attachment) + `" alt="` + esc(thumb.Title) + `"></figure>` + body
			}
		}
		if (strings.EqualFold(i.Slug, "guestbook") || strings.EqualFold(i.Title, "guestbook")) && !strings.Contains(body, `data-sh-feature="guestbook"`) {
			body += c.widget(FeatureGuestbook, "Guestbook", i.Path, nil)
		}
		if i.Type == "forum" {
			body += c.widget(FeatureForum, "bbPress", i.Path, nil)
		}
		if i.Type == "topic" {
			body += c.earlierReplies(i)
		}
		if i.Type == "post" {
			body = dateHTML(i.Date) + c.byline(i, authors, multiAuthor) + body
		}
		body += c.comments(i)
		pageMenu := c.pageMenu(i)
		c.putPage(i.Path, i.Title, excerpt(i), body, pageMenu)
		if front == i && i.Path != "/" {
			c.pageLang["/"] = c.pageLang[i.Path]
			c.putPage("/", i.Title, excerpt(i), body, pageMenu)
		}
	}
	if c.features[FeatureForum] == nil {
		for _, i := range c.published {
			if i.Type == "topic" || i.Type == "reply" {
				c.putPage("/forum/", "Discussion", "", c.widget(FeatureForum, "bbPress", "/forum/", nil), c.resolveMenuForPage(menu, nil))
				break
			}
		}
	}
	postsTitle, postsIntro := "Posts", ""
	for _, i := range c.published {
		if i.Path == blog && i.Type == "page" {
			postsTitle = i.Title
			if strings.TrimSpace(i.Content) != "" {
				postsIntro = c.renderContent(i.Content, i.Path, 0)
			}
			break
		}
	}
	c.archive(blog, postsTitle, postsIntro, c.posts, menu)
	c.authorArchives(authors, menu)
	keys := sortedKeys(c.terms)
	for _, k := range keys {
		t := c.terms[k]
		var prefix string
		switch t.Taxonomy {
		case "category":
			prefix = "category"
			c.result.Report.Categories++
		case "product_cat":
			prefix = "product-category"
		case "post_tag":
			prefix = "tag"
			c.result.Report.Tags++
		default:
			continue
		}
		var posts []*item
		candidates := c.posts
		if t.Taxonomy == "product_cat" {
			candidates = c.published
		}
		for _, i := range candidates {
			for _, a := range i.Categories {
				if a.Domain == t.Taxonomy && (a.Slug == t.Slug || t.Taxonomy == "category" && c.termDescendant(a.Slug, t.Slug)) {
					posts = append(posts, i)
					break
				}
			}
		}
		// Child slugs come straight from the export; clean the whole path.
		p := cleanPath("/" + prefix + "/" + c.termPath(t, map[string]bool{}) + "/")
		c.archive(p, t.Name, "", posts, menu)
	}
	c.search(menu)
	c.putPage("/404.html", "Page not found", "", "<p>This page could not be found. <a href=\"/\">Go home</a> or <a href=\"/search/\">search this site</a>.</p>", c.resolveMenuForPage(menu, nil))
	var sitemap strings.Builder
	sitemap.WriteString(`<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for _, p := range sortedKeys(c.result.Files) {
		if strings.HasSuffix(p, "index.html") {
			sitemap.WriteString("<url><loc>" + esc("/"+strings.TrimSuffix(p, "index.html")) + "</loc></url>")
		}
	}
	sitemap.WriteString("</urlset>")
	c.result.Files["sitemap.xml"] = []byte(sitemap.String())
	c.result.Files["robots.txt"] = []byte("User-agent: *\nAllow: /\n")
	for _, k := range sortedKeys(c.features) {
		f := c.features[k]
		sort.Strings(f.Pages)
		c.configureFeature(f)
		c.result.Report.Features = append(c.result.Report.Features, *f)
	}
	if f := c.features[FeatureComments]; f != nil && f.Moderated {
		for p, b := range c.result.Files {
			if strings.HasSuffix(p, ".html") {
				doc, err := xhtml.Parse(strings.NewReader(string(b)))
				if err != nil {
					continue
				}
				walk(doc, func(n *xhtml.Node) {
					if getAttr(n, "data-sh-feature") == "comments" {
						setAttr(n, "data-moderated", "true")
					}
				})
				var rendered strings.Builder
				_ = xhtml.Render(&rendered, doc)
				c.result.Files[p] = []byte(rendered.String())
			}
		}
	}

	if len(c.features) > 1 {
		c.result.Files["sh-import/features.js"] = []byte(featuresJS)
	}
	if c.features[FeatureShop] != nil {
		c.result.Files["sh-import/payment-links.json"] = []byte("{}")
	}
	if len(c.externalImages) > 0 {
		c.warn(fmt.Sprintf("Kept %d external content image URLs at their original addresses: %s", len(c.externalImages), strings.Join(sortedKeys(c.externalImages), ", ")))
	}
	c.fitBudget()
	c.dropUnsafePaths()
	return c.result, nil
}
func isPageType(s string) bool {
	switch s {
	case "page", "post", "product", "forum", "topic", "reply":
		return true
	}
	return false
}
func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
func (c *converter) warn(s string) { c.result.Report.Warnings = append(c.result.Report.Warnings, s) }
func (c *converter) permalink(i *item, seen map[string]bool) string {
	if seen[i.ID] {
		return "/p/" + i.ID + "/"
	}
	seen[i.ID] = true
	if u, e := url.Parse(i.Link); e == nil {
		for _, key := range []string{"p", "page_id"} {
			if id := u.Query().Get(key); id != "" {
				c.warn("Plain permalink for " + i.Title + " moved to /p/" + id + "/.")
				return cleanPath("/p/" + id + "/")
			}
		}
		if i.Type == "page" && (u.Path == "/" || u.Host != "" && u.Path == "") {
			return "/"
		}
		if u.Path != "" && u.Path != "/" {
			return cleanPath(u.Path)
		}
	}
	slug := i.Slug
	if slug == "" {
		slug = "p/" + i.ID
	}
	if parent := c.byID[i.Parent]; i.Type == "page" && parent != nil && parent != i {
		return cleanPath(c.permalink(parent, seen) + slug + "/")
	}
	return cleanPath("/" + slug + "/")
}
func (c *converter) frontPage() (*item, string) {
	var front *item
	blog := "/"
	for _, i := range c.published {
		if i.Type == "page" && i.Path == "/" {
			front = i
			break
		}
	}
	if id := pageIDClass.FindStringSubmatch(c.bodyClasses); front == nil && len(id) > 1 {
		front = c.byID[id[1]]
		if front != nil && (front.Type != "page" || !front.public()) {
			front = nil
		}
	}
	isBlog := strings.Contains(" "+c.bodyClasses+" ", " home ") && strings.Contains(" "+c.bodyClasses+" ", " blog ")
	if front == nil && !isBlog {
		for _, i := range c.published {
			if i.Type == "page" && (i.Slug == "home" || i.Slug == "homepage" || i.Slug == "front-page" || i.Slug == "welcome" || strings.EqualFold(i.Title, "Home")) {
				front = i
				break
			}
		}
	}
	if front != nil {
		blog = "/blog/"
		if p := c.postsPage(); p != nil {
			blog = p.Path
		}
		c.result.Report.FrontPage = "page:" + front.Path
	} else {
		c.result.Report.FrontPage = "posts"
	}
	return front, blog
}

var postsPageSlugs = map[string]bool{"blog": true, "news": true, "posts": true, "journal": true, "articles": true, "updates": true, "stories": true}

// postsPage picks the public page that stands in for WordPress's "posts
// page" setting (the export does not say which page that is): among pages
// slugged like a posts index, prefer one that is in the menu, then one with
// no content of its own, then the first found.
func (c *converter) postsPage() *item {
	var best *item
	bestScore := -1
	for _, i := range c.published {
		if i.Type != "page" || !postsPageSlugs[i.Slug] {
			continue
		}
		score := 0
		if menuHasPath(c.headerMenu, i.Path) {
			score += 2
		}
		if strings.TrimSpace(i.Content) == "" {
			score++
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	return best
}

// pageMenu records i's language for its own output path (so putPage can set
// <html lang>) and returns the menu to show on that page: its own
// language's menu when the site has more than one language, with
// Polylang's dead language-switcher link turned into real links to i's
// translations.
func (c *converter) pageMenu(i *item) []*menuItem {
	lang := "en"
	m := c.headerMenu
	if c.multiLang {
		lang = c.defaultLang
		if l := i.language(); l != "" {
			lang = l
		}
		m = c.menuFor(lang)
	}
	if c.pageLang == nil {
		c.pageLang = map[string]string{}
	}
	c.pageLang[i.Path] = lang
	return c.resolveMenuForPage(m, i)
}

// authorData is one author's archive: where it lives and how to label it.
type authorData struct {
	login, nicename, display, path string
}

// authorInfo finds every author with at least one public post. The bool is
// whether the site has more than one such author, which decides whether
// post pages show a by-line at all.
func (c *converter) authorInfo() (map[string]*authorData, bool) {
	byLogin := map[string]author{}
	for _, a := range c.data.Authors {
		byLogin[a.Login] = a
	}
	counts := map[string]int{}
	for _, i := range c.posts {
		if i.Author != "" {
			counts[i.Author]++
		}
	}
	out := map[string]*authorData{}
	for login := range counts {
		a := byLogin[login]
		nicename := a.nicename()
		if nicename == "" {
			nicename = "author"
		}
		display := a.DisplayName
		if display == "" {
			display = login
		}
		out[login] = &authorData{login: login, nicename: nicename, display: display, path: cleanPath("/author/" + nicename + "/")}
	}
	return out, len(counts) > 1
}

// byline is the "By <name>" link shown next to a post's date, or "" when
// the site has one author (or none recorded) and so has nothing to say.
func (c *converter) byline(i *item, authors map[string]*authorData, multiAuthor bool) string {
	if !multiAuthor || i.Author == "" {
		return ""
	}
	a := authors[i.Author]
	if a == nil {
		return ""
	}
	return ` <span class="byline">By <a href="` + esc(relative(i.Path, a.path)) + `">` + esc(a.display) + `</a></span>`
}

// authorArchives writes /author/<nicename>/ for every author with public
// posts (even when there is only one, so an old link to it keeps working).
func (c *converter) authorArchives(authors map[string]*authorData, menu []*menuItem) {
	for _, login := range sortedKeys(authors) {
		a := authors[login]
		var posts []*item
		for _, i := range c.posts {
			if i.Author == login {
				posts = append(posts, i)
			}
		}
		c.archive(a.path, "Posts by "+a.display, "", posts, menu)
	}
}
func (c *converter) termPath(t term, seen map[string]bool) string {
	if seen[t.Slug] {
		return t.Slug
	}
	seen[t.Slug] = true
	if p, ok := c.terms[t.Taxonomy+":"+t.Parent]; ok && p.Slug != "" {
		return c.termPath(p, seen) + "/" + t.Slug
	}
	return strings.Trim(strings.TrimPrefix(cleanPath(t.Slug), "/"), "/")
}
func (c *converter) termDescendant(slug, parent string) bool {
	seen := map[string]bool{}
	for slug != "" && !seen[slug] {
		if slug == parent {
			return true
		}
		seen[slug] = true
		slug = c.terms["category:"+slug].Parent
	}
	return false
}
func (c *converter) archive(p, title, intro string, items []*item, menu []*menuItem) {
	pages := (len(items) + 19) / 20
	if pages == 0 {
		pages = 1
	}
	for n := 0; n < pages; n++ {
		start, end := n*20, (n+1)*20
		if end > len(items) {
			end = len(items)
		}
		body := c.postList(items[start:end])
		if n == 0 && intro != "" {
			body = intro + body
		}
		body += `<nav aria-label="Archive pages">`
		for k := 0; k < pages; k++ {
			u := p
			if k > 0 {
				u += fmt.Sprintf("page/%d/", k+1)
			}
			body += `<a href="` + esc(u) + `">` + fmt.Sprint(k+1) + `</a> `
		}
		body += `</nav>`
		u := p
		if n > 0 {
			u += fmt.Sprintf("page/%d/", n+1)
		}
		c.putPage(u, title, c.data.Description, body, c.resolveMenuForPage(menu, nil))
	}
}
func (c *converter) postList(items []*item) string {
	var b strings.Builder
	for _, i := range items {
		b.WriteString(`<article><h2><a href="` + esc(i.Path) + `">` + esc(i.Title) + `</a></h2>` + dateHTML(i.Date) + `<p>` + esc(excerpt(i)) + `</p></article>`)
	}
	return b.String()
}
func (c *converter) latest() string {
	n := len(c.posts)
	if n > 5 {
		n = 5
	}
	return c.postList(c.posts[:n])
}
func (c *converter) putPage(p, title, description, body string, menu []*menuItem) {
	body = c.cleanContent(body, p)
	walk(fragment(body), func(n *xhtml.Node) {
		if n.Data == "form" || n.Data == "section" || n.Data == "a" {
			kind := getAttr(n, "data-sh-feature")
			if kind == "contact" {
				kind = FeatureContactForm
			}
			if kind == "buy" {
				kind = FeatureShop
			}
			if c.features[kind] != nil {
				c.feature(kind, "", p, 0)
			}
		}
	})
	footer := c.cleanContent(strings.ReplaceAll(c.footer, `data-page="`+footerPage+`"`, `data-page="`+esc(p)+`"`), p)
	head := ""
	for _, f := range c.themeLinks {
		head += `<link rel="stylesheet" href="` + esc(relative(p, f)) + `">`
	}
	head += `<link rel="stylesheet" href="` + esc(relative(p, "sh-import/theme.css")) + `">`
	scripts := ""
	if strings.Contains(body+footer, "data-sh-feature=") {
		config, _ := json.Marshal(map[string]string{"site": c.in.Site})
		scripts = `<script>window.SH_CONFIG = ` + string(config) + `;</script><script src="https://simple-host.app/auth.js"></script><script src="` + esc(relative(p, "sh-import/features.js")) + `" defer></script>`
	}
	titleHTML := `<h1 class="entry-title">` + esc(title) + `</h1>`
	if p == "/" && strings.HasPrefix(c.result.Report.FrontPage, "page:") {
		titleHTML = ""
	}
	lang := c.defaultLang
	if l, ok := c.pageLang[p]; ok && l != "" {
		lang = l
	}
	if lang == "" {
		lang = "en"
	}
	h := `<!doctype html><html lang="` + esc(lang) + `"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>` + esc(title+" - "+c.data.Title) + `</title><meta name="description" content="` + esc(truncate(plain(description), 300)) + `">` + head + `</head><body class="` + esc(c.bodyClasses) + `"><div class="wp-site-blocks"><header class="site-header"><a class="site-title" href="` + relative(p, "/") + `">` + esc(c.data.Title) + `</a>` + c.responsiveNav(menu, p, "main-navigation", true) + `</header><main class="site-main"><article>` + titleHTML + `<div class="entry-content">` + body + `</div></article></main><footer>` + footer + `<p>Moved from WordPress, hosted on Simple Host</p></footer></div>` + scripts + `</body></html>`
	c.result.Files[filePath(p)] = []byte(h)
}
func (c *converter) search(menu []*menuItem) {
	type entry struct {
		Path    string `json:"path"`
		Title   string `json:"title"`
		Date    string `json:"date"`
		Excerpt string `json:"excerpt"`
		Text    string `json:"text"`
	}
	var entries []entry
	for _, i := range c.published {
		entries = append(entries, entry{i.Path, i.Title, i.Date, truncate(excerpt(i), 300), truncate(cleanText(i.Content), 5000)})
	}
	b, _ := json.Marshal(entries)
	c.result.Files["search-index.json"] = b
	c.putPage("/search/", "Search", "", c.searchForm()+`<div id="search-results" aria-live="polite"></div>`, c.resolveMenuForPage(menu, nil))
	h := string(c.result.Files["search/index.html"])
	script := `<script>(function(){var q=document.querySelector('[name=q]'),out=document.getElementById('search-results'),items=[];q.value=new URLSearchParams(location.search).get('q')||'';function show(){out.textContent='';var term=q.value.toLowerCase().trim();if(!term)return;items.filter(function(x){return (x.title+' '+x.text).toLowerCase().includes(term)}).forEach(function(x){var a=document.createElement('a'),p=document.createElement('p');a.href='../'+x.path.replace(/^\//,'');a.textContent=x.title;p.textContent=x.excerpt;out.append(a,p)})}fetch('../search-index.json').then(function(r){return r.json()}).then(function(x){items=x;show()}).catch(function(){out.textContent='Search could not be loaded.'});q.addEventListener('input',show);q.form.addEventListener('submit',function(e){e.preventDefault();show()})})();</script>`
	c.result.Files["search/index.html"] = []byte(strings.Replace(h, "</body>", script+"</body>", 1))
}
func (c *converter) searchForm() string {
	return `<form data-sh-search role="search" action="/search/" method="get"><label>Search <input name="q" type="search"></label><button>Search</button></form>`
}
func (c *converter) earlierReplies(i *item) string {
	var b strings.Builder
	for _, r := range c.published {
		if r.Type == "reply" && r.Parent == i.ID {
			b.WriteString(`<article><h2>` + esc(r.Author) + `</h2>` + c.renderContent(r.Content, r.Path, 0) + `</article>`)
		}
	}
	return b.String()
}

// dropUnsafePaths is the last guard on output keys: every path the export can
// influence is cleaned where it is built, and anything that still is not a
// plain relative path inside the site is left out with a warning.
func (c *converter) dropUnsafePaths() {
	for p := range c.result.Files {
		bad := p == "" || len(p) > 1024 || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00") || path.Clean(p) != p
		for _, part := range strings.Split(p, "/") {
			bad = bad || part == ".." || part == "." || len(part) > 255
		}
		if bad {
			delete(c.result.Files, p)
			c.result.Report.Warnings = append(c.result.Report.Warnings, "Left out a file with an unusable path: "+strings.ToValidUTF8(p[:min(len(p), 120)], "?"))
		}
	}
}
