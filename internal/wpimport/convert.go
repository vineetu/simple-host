package wpimport

import (
	"context"
	"encoding/json"
	"fmt"
	xhtml "golang.org/x/net/html"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

type converter struct {
	ctx                context.Context
	in                 Input
	limits             Limits
	data               *export
	result             *Result
	byID               map[string]*item
	published          []*item
	posts              []*item
	variationsByParent map[string][]*item
	postsByAuthor      map[string][]*item
	eventsCache        []*item
	eventsReady        bool
	pagePaths          map[string]bool
	archiveTerms       map[string]bool
	terms              map[string]term
	hosts              map[string]bool
	media              map[string]string
	mediaOriginal      map[string]string
	features           map[string]*Feature
	themeLinks         []string
	bodyClasses        string
	homepage           string
	postsPath          string
	headerMenu         []*menuItem
	headerRef          string
	footer             string
	externalImages     map[string]bool
	mediaNotes         map[string]*fileNote
	formMissing        map[string][]string
	perLangMenu        map[string][]*menuItem
	languages          map[string]bool
	multiLang          bool
	defaultLang        string
	frontTranslations  map[string]*item
	pageLang           map[string]string
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
	c.result.Report.Platform = platformOf(d)
	if title := siteTitle(d); title != d.Title {
		d.Title = title
		c.result.Report.SiteTitle = title
	}
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
	otherTypes := map[string]int{}
	systemPages := map[string][]string{}
	c.archiveTerms = map[string]bool{}
	for _, i := range d.Items {
		if i.ID != "" && c.byID[i.ID] != i {
			continue
		}
		if i.Status == "publish" && !i.public() && c.carries(i) {
			protected++
		}
		if i.public() && !c.carries(i) && !internalType(i.Type) {
			otherTypes[i.Type]++
		}
		if i.public() && c.carries(i) {
			if kind := systemPage(i); kind != "" {
				systemPages[kind] = append(systemPages[kind], c.permalink(i, map[string]bool{}))
				continue
			}
			i.Path = c.permalink(i, map[string]bool{})
			c.published = append(c.published, i)
			if i.Type == "post" {
				c.posts = append(c.posts, i)
				c.result.Report.Posts++
			} else if isPageType(i.Type) {
				c.result.Report.Pages++
			} else {
				c.result.Report.OtherItems++
			}
		}
		if !i.public() || !c.carries(i) {
			continue
		}
		for _, t := range i.Categories {
			if t.Domain == "category" || t.Domain == "post_tag" || t.Domain == "product_cat" {
				k := t.Domain + ":" + t.Slug
				c.archiveTerms[k] = true
				if _, ok := c.terms[k]; !ok {
					c.terms[k] = term{Taxonomy: t.Domain, Slug: t.Slug, Name: t.Name}
				}
			}
		}
	}
	for _, kind := range sortedKeys(systemPages) {
		label, reason := "shop's cart, checkout, and account", "online checkout"
		if kind == FeatureDonation {
			label, reason = "donation receipt and dashboard", "online donations"
		}
		verb := "does"
		if kind == FeatureDonation {
			verb = "do"
		}
		c.warn("Left out the " + label + " pages (" + strings.Join(systemPages[kind], ", ") + "), because " + reason + " " + verb + " not move to Simple Host.")
	}
	if protected > 0 {
		if protected == 1 {
			c.warn("Left out 1 password-protected page or post; it stays private and is not on the new site.")
		} else {
			c.warn(fmt.Sprintf("Left out %d password-protected pages or posts; they stay private and are not on the new site.", protected))
		}
	}
	if len(otherTypes) > 0 {
		n := 0
		for _, k := range sortedKeys(otherTypes) {
			n += otherTypes[k]
		}
		c.warn(fmt.Sprintf("Left out %d items that plugins stored in the export (%s). They can hold private details such as orders or form entries, so they are not published; ask the chat if some of them should be pages.", n, strings.Join(sortedKeys(otherTypes), ", ")))
	}
	if len(c.published) == 0 {
		return nil, fmt.Errorf("export has no published pages or posts")
	}
	c.detectBuilders()
	c.squarespaceNotes()
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
	c.defaultLang = siteLanguage(d.Language)
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
	c.feature(FeatureSearch, c.searchSource(), "/search/", 1)
	for _, i := range c.data.Items {
		if i.Type == "jp_pay_product" && i.public() {
			c.feature(FeatureShop, "Jetpack payments", "/", 0)
		}
	}
	authors, multiAuthor := c.authorInfo()
	postsTitle, postsIntro := "Posts", ""
	var postsPage *item
	for n, i := range c.published {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if in.Progress != nil {
			in.Progress("Building pages", n, len(c.published))
		}
		body := c.renderContent(i.Content, i.Path, 0)
		if i.Type == "product" {
			body = c.product(i, i.Path) + c.renderContent(i.Excerpt, i.Path, 0) + body
			if ids := i.metadata("_product_image_gallery"); ids != "" {
				body = c.gallery(ids, "3", "large", "") + body
			}
		}
		if i.Type == "tribe_events" {
			body = c.eventDetails(i) + body
		}
		if i.Type == "page" && (i.Slug == "shop" || i.Slug == "store") && strings.TrimSpace(i.Content) == "" && len(c.products()) > 0 {
			body += c.productList(c.products())
		}
		if i.Type == "page" && i.Path == "/events/" {
			body += c.eventList()
		}
		if thumb := c.attachment(i.metadata("_thumbnail_id")); thumb != nil {
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
		if i.Type == "page" && i.Path == blog {
			postsTitle, postsIntro = i.Title, body
			postsPage = i
		}
		c.putPage(i.Path, i.Title, excerpt(i), body, pageMenu)
		if front == i && i.Path != "/" {
			c.pageLang["/"] = c.pageLang[i.Path]
			c.putPage("/", i.Title, excerpt(i), body, pageMenu)
		}
	}
	eventsPage := false
	for _, i := range c.published {
		eventsPage = eventsPage || i.Path == "/events/"
	}
	if len(c.events()) > 0 && !eventsPage {
		c.putPage("/events/", "Events", "", c.eventList(), c.archiveMenu("/events/", c.defaultLang, menu))
	}
	if c.features[FeatureForum] == nil {
		for _, i := range c.published {
			if i.Type == "topic" || i.Type == "reply" {
				c.putPage("/forum/", "Discussion", "", c.widget(FeatureForum, "bbPress", "/forum/", nil), c.archiveMenu("/forum/", c.defaultLang, menu))
				break
			}
		}
	}
	if c.pageLang[blog] == "" {
		c.archiveMenu(blog, c.defaultLang, menu)
	}
	c.archive(blog, postsTitle, postsIntro, c.posts, menu, postsPage)
	c.squarespaceSections(blog, menu)
	c.authorArchives(authors, menu)
	keys := sortedKeys(c.terms)
	for _, k := range keys {
		t := c.terms[k]
		if !c.archiveTerms[k] {
			continue
		}
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
			candidates = c.products()
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
	c.putPage("/404.html", "Page not found", "", "<p>This page could not be found. <a href=\"/\">Go home</a> or <a href=\"/search/\">search this site</a>.</p>", c.archiveMenu("/404.html", c.defaultLang, menu))
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
	if c.features[FeatureShop] != nil || c.features[FeatureDonation] != nil {
		c.result.Files["sh-import/payment-links.json"] = []byte("{}")
	}
	if n := len(c.externalImages); n > 0 {
		urls := sortedKeys(c.externalImages)
		if len(urls) > 5 {
			urls = urls[:5]
		}
		c.warn(fmt.Sprintf("%d images are still loaded from other websites: %s.", n, strings.Join(urls, ", ")))
	}
	if n := c.result.Report.Dropped["external_embed"]; n > 0 {
		c.warn(fmt.Sprintf("Left out %d embedded frames from other websites (only YouTube, Vimeo, and maps are kept).", n))
	}
	galleries := map[string]string{"gallery:foogallery": "FooGallery", "gallery:envira": "Envira Gallery", "shortcode:modula": "Modula", "shortcode:robo-gallery": "Robo Gallery", "shortcode:ngg": "NextGEN Gallery", "shortcode:nggallery": "NextGEN Gallery", "shortcode:ngg_images": "NextGEN Gallery"}
	lost, plugins := 0, map[string]bool{}
	for _, k := range sortedKeys(galleries) {
		if n := c.result.Report.Dropped[k]; n > 0 {
			lost += n
			plugins[galleries[k]] = true
		}
	}
	if lost > 0 {
		what := fmt.Sprintf("%d photo galleries", lost)
		if lost == 1 {
			what = "1 photo gallery"
		}
		c.warn(fmt.Sprintf("%s could not be rebuilt (%s), because the plugin keeps the photo list outside the WordPress export. Ask the chat to add those photos to the pages.", what, strings.Join(sortedKeys(plugins), ", ")))
	}
	c.fitBudget()
	c.dropUnsafePaths()
	return c.result, nil
}
func (c *converter) squarespace() bool {
	return c.result != nil && c.result.Report.Platform == "Squarespace"
}
func (c *converter) searchSource() string {
	if c.squarespace() {
		return "built-in search"
	}
	return "WordPress search"
}
func (c *converter) commentSource() string {
	if c.squarespace() {
		return "Squarespace comments"
	}
	return "WordPress comments"
}

// platformOf reports Squarespace only when the channel has neither base URL
// (Squarespace's export omits both) and the file looks like Squarespace.
// WordPress.com is recognised from its generator line; everything else is
// WordPress, including a WordPress file that merely mentions Squarespace.
func platformOf(d *export) string {
	if !d.HasBase && !d.HasBlog && squarespaceExport(d) {
		return "Squarespace"
	}
	if strings.Contains(strings.ToLower(d.Generator), "wordpress.com") {
		return "WordPress.com"
	}
	return "WordPress"
}

// squarespaceMarkup matches Squarespace's own block classes and image
// sources, not a post that merely mentions a Squarespace address.
var squarespaceMarkup = regexp.MustCompile(`class="[^"]*\bsqs-(?:block|html-content|layout)|(?:src|data-src|data-image)="https?://(?:images\.squarespace-cdn\.com|static1?\.squarespace\.com)/`)

func squarespaceExport(d *export) bool {
	if u, err := url.Parse(strings.TrimSpace(d.Link)); err == nil && strings.HasSuffix(strings.ToLower(u.Hostname()), ".squarespace.com") {
		return true
	}
	for _, i := range d.Items {
		s := i.Content + "\n" + i.Excerpt
		if squarespaceMarkup.MatchString(s) {
			return true
		}
	}
	return false
}

// siteTitle replaces Squarespace's untouched SEO placeholder, or an empty
// title, with the channel link's host (www. removed).
func siteTitle(d *export) string {
	t := strings.TrimSpace(d.Title)
	if t != "" && t != "Your Site Title" {
		return d.Title
	}
	if u, err := url.Parse(strings.TrimSpace(d.Link)); err == nil {
		if h := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www."); h != "" {
			return h
		}
	}
	return d.Title
}
func (c *converter) squarespaceNotes() {
	if !c.squarespace() {
		return
	}
	c.warn("Squarespace's export leaves out store, event, portfolio, album and index pages, every blog except one, audio, video and product blocks, and form fields. Pages missing here can be added by asking the chat; products can come from Squarespace's product CSV export (Commerce > Inventory > Export).")
	c.warn("Squarespace's design (fonts, colours, layout) does not come over, so the site uses a plain built-in look. Ask the chat to restyle it.")
	n := 0
	for _, i := range c.published {
		if i.Type == "page" && squarespaceLayout(i.Content) {
			n++
		}
	}
	if n == 1 {
		c.warn("1 page used Squarespace's page layout. Its words and photos came over in one column; ask the chat to adjust anything.")
	} else if n > 1 {
		c.warn(fmt.Sprintf("%d pages used Squarespace's page layout. Their words and photos came over in one column; ask the chat to adjust anything.", n))
	}
}
func squarespaceLayout(s string) bool {
	if strings.Contains(s, "fe-block") || strings.Contains(s, "fluid-engine") {
		return true
	}
	if !strings.Contains(s, "sqs-row") {
		return false
	}
	return strings.Count(s, "sqs-col") >= 2
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
		if front != nil && (front.Type != "page" || !front.public() || front.Path == "") {
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
		} else {
			occupied := map[string]bool{}
			for _, i := range c.published {
				occupied[i.Path] = true
			}
			for occupied[blog] {
				blog += "posts/"
			}
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
	lang := c.defaultLang
	if lang == "" {
		lang = "en"
	}
	m := c.headerMenu
	if c.multiLang {
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
	used := map[string]bool{}
	for _, login := range sortedKeys(counts) {
		a := byLogin[login]
		if a.Login == "" {
			a.Login = login
		}
		nicename := a.nicename()
		if nicename == "" {
			nicename = "author"
		}
		for base, n := nicename, 2; used[nicename]; n++ {
			nicename = fmt.Sprintf("%s-%d", base, n)
		}
		used[nicename] = true
		display := strings.TrimSpace(a.DisplayName)
		if display == "" {
			display = login
		}
		// A display name that is an email address, or a login used in its
		// place, must never be printed. The archive still exists.
		if strings.Contains(display, "@") {
			display = ""
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
	if a == nil || a.display == "" {
		return ""
	}
	return ` <span class="byline">By <a href="` + esc(relative(i.Path, a.path)) + `">` + esc(a.display) + `</a></span>`
}

// authorArchives writes /author/<nicename>/ for every author with public
// posts (even when there is only one, so an old link to it keeps working).
func (c *converter) authorArchives(authors map[string]*authorData, menu []*menuItem) {
	if c.postsByAuthor == nil {
		c.postsByAuthor = map[string][]*item{}
		for _, i := range c.posts {
			c.postsByAuthor[i.Author] = append(c.postsByAuthor[i.Author], i)
		}
	}
	for _, login := range sortedKeys(authors) {
		a := authors[login]
		posts := c.postsByAuthor[login]
		lang := c.defaultLang
		if len(posts) > 0 {
			lang = c.itemLanguage(posts[0])
		}
		c.archiveMenu(a.path, lang, menu)
		title := "Posts"
		if a.display != "" {
			title = "Posts by " + a.display
		}
		c.archive(a.path, title, "", posts, menu)
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
func (c *converter) itemLanguage(i *item) string {
	if c.multiLang && i.language() != "" {
		return i.language()
	}
	return c.defaultLang
}

func (c *converter) archiveMenu(p, lang string, menu []*menuItem) []*menuItem {
	if lang == "" {
		lang = c.defaultLang
	}
	if c.pageLang == nil {
		c.pageLang = map[string]string{}
	}
	c.pageLang[p] = lang
	if c.multiLang {
		menu = c.menuFor(lang)
	}
	return c.resolveMenuForPage(menu, nil)
}

func (c *converter) archive(p, title, intro string, items []*item, menu []*menuItem, pagesFor ...*item) {
	lang := c.pageLang[p]
	if lang == "" {
		lang = c.defaultLang
		if c.multiLang && len(items) > 0 {
			lang = c.itemLanguage(items[0])
		}
	}
	if c.multiLang {
		var localized []*item
		for _, i := range items {
			if c.itemLanguage(i) == lang {
				localized = append(localized, i)
			}
		}
		items = localized
	}
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
		if strings.HasPrefix(p, "/product-category/") {
			body = c.productList(items[start:end])
		}
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
		pageMenu := c.archiveMenu(u, lang, menu)
		description := c.data.Description
		if len(pagesFor) > 0 && pagesFor[0] != nil {
			i := pagesFor[0]
			description = excerpt(i)
			m := menu
			if c.multiLang {
				m = c.menuFor(lang)
			}
			pageMenu = c.resolveMenuForPage(m, i)
		}
		c.putPage(u, title, description, body, pageMenu)
	}
}
func (c *converter) postList(items []*item) string {
	var b strings.Builder
	for _, i := range items {
		date := ""
		if i.Type == "post" {
			date = dateHTML(i.Date)
		}
		b.WriteString(`<article><h2><a href="` + esc(i.Path) + `">` + esc(i.Title) + `</a></h2>` + date + `<p>` + esc(excerpt(i)) + `</p></article>`)
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

// siteLanguage keeps the channel language when it is a short tag of letters,
// digits and hyphens, and uses en when the export left it empty or invalid.
func siteLanguage(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if b.Len() >= 35 {
			break
		}
		if r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	s := strings.Trim(b.String(), "-")
	if !validLangTag(s) {
		return "en"
	}
	return s
}

func validLangTag(s string) bool {
	parts := strings.Split(s, "-")
	if len(parts[0]) < 2 || len(parts[0]) > 3 {
		return false
	}
	for _, r := range parts[0] {
		if r < 'A' || (r > 'Z' && r < 'a') || r > 'z' {
			return false
		}
	}
	for _, part := range parts[1:] {
		if len(part) < 2 || len(part) > 8 {
			return false
		}
		for _, r := range part {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
				return false
			}
		}
	}
	return true
}

// rtlLanguage reports tags whose primary subtag is written right to left.
func rtlLanguage(tag string) bool {
	primary := strings.ToLower(tag)
	if i := strings.IndexByte(primary, '-'); i >= 0 {
		primary = primary[:i]
	}
	switch primary {
	case "ar", "he", "iw", "fa", "ur", "ps", "sd", "ug", "yi", "dv", "ckb":
		return true
	}
	return false
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
				if getAttr(n, "data-product") == "donate" && hasAncestorClass(n, "sh-donate") {
					kind = FeatureDonation
				}
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
	htmlTag := `<html lang="` + esc(lang) + `"`
	if rtlLanguage(lang) {
		htmlTag += ` dir="rtl"`
	}
	htmlTag += `>`
	h := `<!doctype html>` + htmlTag + `<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>` + esc(title+" - "+c.data.Title) + `</title><meta name="description" content="` + esc(truncate(plain(description), 300)) + `">` + head + `</head><body class="` + esc(c.bodyClasses) + `"><div class="wp-site-blocks"><header class="site-header"><a class="site-title" href="` + relative(p, "/") + `">` + esc(c.data.Title) + `</a>` + c.responsiveNav(menu, p, "main-navigation", true) + `</header><main class="site-main"><article>` + titleHTML + `<div class="entry-content">` + body + `</div></article></main><footer>` + footer + `<p>Moved from WordPress, hosted on Simple Host</p></footer></div>` + scripts + `</body></html>`
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
	c.putPage("/search/", "Search", "", c.searchForm()+`<div id="search-results" aria-live="polite"></div>`, c.archiveMenu("/search/", c.defaultLang, menu))
	h := string(c.result.Files["search/index.html"])
	script := `<script>(function(){var q=document.querySelector('[name=q]'),out=document.getElementById('search-results'),items=[];q.value=new URLSearchParams(location.search).get('q')||'';function show(){out.textContent='';var term=q.value.toLowerCase().trim();if(!term)return;items.filter(function(x){return (x.title+' '+x.text).toLowerCase().includes(term)}).forEach(function(x){var a=document.createElement('a'),p=document.createElement('p');a.href='../'+x.path.replace(/^\//,'');a.textContent=x.title;p.textContent=x.excerpt;out.append(a,p)})}fetch('../search-index.json').then(function(r){return r.json()}).then(function(x){items=x;show()}).catch(function(){out.textContent='Search could not be loaded.'});q.addEventListener('input',show);q.form.addEventListener('submit',function(e){e.preventDefault();show()})})();</script>`
	c.result.Files["search/index.html"] = []byte(strings.Replace(h, "</body>", script+"</body>", 1))
}
func (c *converter) searchForm() string {
	return `<form data-sh-search role="search" action="/search/" method="get"><label>Search <input name="q" type="search"></label><button>Search</button></form>`
}
func (c *converter) earlierReplies(i *item) string {
	var b strings.Builder
	var authors map[string]*authorData
	for _, r := range c.published {
		if r.Type == "reply" && r.Parent == i.ID {
			name := r.Author
			if strings.Contains(name, "@") {
				if authors == nil {
					authors, _ = c.authorInfo()
				}
				name = ""
				if a := authors[r.Author]; a != nil {
					name = a.display
				}
			}
			heading := ""
			if name != "" {
				heading = `<h2>` + esc(name) + `</h2>`
			}
			b.WriteString(`<article>` + heading + c.renderContent(r.Content, r.Path, 0) + `</article>`)
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

// pageBuilder is one page-builder plugin the importer can recognise from
// postmeta or raw shortcodes left in the export. missingContent marks
// builders (Oxygen, Bricks) that keep their words outside post_content, so
// the moved page may come over empty rather than merely unstyled.
type pageBuilder struct {
	name           string
	missingContent bool
	match          func(*item) bool
}

var pageBuilders = []pageBuilder{
	{"Elementor", false, func(i *item) bool {
		return i.metadata("_elementor_edit_mode") == "builder" || i.metadata("_elementor_data") != ""
	}},
	{"Divi", false, func(i *item) bool {
		return i.metadata("_et_pb_use_builder") == "on" || strings.Contains(i.Content, "[et_pb_")
	}},
	{"WPBakery", false, func(i *item) bool { return strings.Contains(i.Content, "[vc_row") }},
	{"Beaver Builder", false, func(i *item) bool { return i.metadata("_fl_builder_enabled") == "1" }},
	{"SiteOrigin", false, func(i *item) bool { return i.metadata("panels_data") != "" }},
	{"Oxygen", true, func(i *item) bool { return i.metadata("ct_builder_shortcodes") != "" }},
	{"Bricks", true, func(i *item) bool { return i.metadata("_bricks_page_content_2") != "" }},
}

// detectBuilders warns once per page-builder plugin found on published
// pages, listing the pages it touched; the builder's own layout never
// survives the move. Words and photos in post_content come over, and Divi
// buttons do too; the builder's layout does not.
func (c *converter) detectBuilders() {
	for _, b := range pageBuilders {
		var pages []string
		for _, i := range c.published {
			if b.match(i) {
				pages = append(pages, i.Path)
			}
		}
		if len(pages) == 0 {
			continue
		}
		sort.Strings(pages)
		were := "pages were"
		if len(pages) == 1 {
			were = "page was"
		}
		list := strings.Join(pages, ", ")
		if b.missingContent {
			c.warn(fmt.Sprintf("%d %s built with %s, which keeps page content outside the WordPress export, so these pages may be empty: %s. Check them and ask the chat to adjust anything.", len(pages), were, b.name, list))
		} else {
			brought := "words and photos"
			if b.name == "Divi" {
				brought = "words, photos and buttons"
			}
			c.warn(fmt.Sprintf("%d %s built with %s: %s. Their %s came over, but %s's layout (columns, spacing, backgrounds, and effects) did not, so they use a simple one-column layout. Check them and ask the chat to adjust anything.", len(pages), were, b.name, list, brought, b.name))
		}
	}
}

// squarespaceSections writes a post list at each Squarespace blog's own
// address (/devlog/, /blog/), which the export does not include as a page,
// so old links and the menu read from the live site keep working.
func (c *converter) squarespaceSections(blog string, menu []*menuItem) {
	if !c.squarespace() {
		return
	}
	taken := map[string]bool{blog: true, "/": true}
	for _, i := range c.published {
		taken[i.Path] = true
	}
	sections := map[string][]*item{}
	for _, i := range c.posts {
		parts := strings.SplitN(strings.Trim(i.Path, "/"), "/", 2)
		if len(parts) == 2 && parts[0] != "" {
			sec := "/" + parts[0] + "/"
			sections[sec] = append(sections[sec], i)
		}
	}
	for _, sec := range sortedKeys(sections) {
		if taken[sec] {
			continue
		}
		name := strings.Trim(sec, "/")
		title := strings.ToUpper(name[:1]) + strings.ReplaceAll(name[1:], "-", " ")
		for _, m := range menu {
			if cleanPath(m.URL) == sec && m.Title != "" {
				title = m.Title
			}
		}
		c.archive(sec, title, "", sections[sec], menu)
	}
}
