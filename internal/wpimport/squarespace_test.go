package wpimport

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"sync"
	"testing"

	xhtml "golang.org/x/net/html"
)

func sqsDoc(title, link, extra, items string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:excerpt="http://wordpress.org/export/1.2/excerpt/" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:wp="http://wordpress.org/export/1.2/">
<channel>
<title>` + title + `</title>
<link>` + link + `</link>
<description></description>
<language>en-US</language>
<wp:wxr_version>1.2</wp:wxr_version>
<wp:author><wp:author_id>1</wp:author_id><wp:author_login>owner@example.com</wp:author_login><wp:author_email>owner@example.com</wp:author_email><wp:author_display_name>Owner</wp:author_display_name></wp:author>
` + extra + items + `
</channel></rss>`
}

func sqsItem(id, title, link, kind, content, extra string) string {
	return `<item>
<title>` + title + `</title>
<link>` + link + `</link>
<dc:creator>owner@example.com</dc:creator>
<content:encoded><![CDATA[` + content + `]]></content:encoded>
<wp:post_id>` + id + `</wp:post_id>
<wp:post_date>2014-02-11 12:00:00</wp:post_date>
<wp:post_name>` + id + `</wp:post_name>
<wp:status>publish</wp:status>
<wp:post_type>` + kind + `</wp:post_type>
` + extra + `
</item>`
}

func sqsBlock(inner string) string {
	return `<div class="sqs-block html-block sqs-block-html"><div class="sqs-block-content"><div class="sqs-html-content">` + inner + `</div></div></div>`
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	im.Set(0, 0, color.NRGBA{10, 20, 30, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func convertSqs(t *testing.T, doc string, fetch func(string) ([]byte, error), theme string) *Result {
	t.Helper()
	r, err := Convert(context.Background(), Input{
		Site:     "example-studio",
		WXR:      []io.Reader{strings.NewReader(doc)},
		Fetch:    func(_ context.Context, raw string) ([]byte, error) { return fetch(raw) },
		ThemeURL: theme,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func warningIs(r *Result, want string) bool {
	for _, w := range r.Report.Warnings {
		if w == want {
			return true
		}
	}
	return false
}

func warningHas(r *Result, part string) bool {
	for _, w := range r.Report.Warnings {
		if strings.Contains(w, part) {
			return true
		}
	}
	return false
}

func navWide(html string) string {
	const open = `<div class="nav-wide">`
	i := strings.Index(html, open)
	if i < 0 {
		return ""
	}
	rest := html[i+len(open):]
	j := strings.Index(rest, "</div>")
	if j < 0 {
		return rest
	}
	return rest[:j]
}

func assertWellFormed(t *testing.T, html string) {
	t.Helper()
	if strings.Contains(html, "<br<") || strings.Contains(html, "<div<") {
		t.Fatalf("broken markup:\n%s", html)
	}
	doc, err := xhtml.Parse(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := xhtml.Render(&b, doc); err != nil {
		t.Fatalf("render: %v\n%s", err, html)
	}
}

func TestSquarespacePlatform(t *testing.T) {
	page := sqsItem("1", "About", "/about", "page", sqsBlock("<p>Example Studio</p>"), "")
	r := convertSqs(t, sqsDoc("Example Studio", "https://www.example.com", "", page), func(string) ([]byte, error) { return nil, nil }, "")
	if r.Report.Platform != "Squarespace" {
		t.Fatalf("platform %q", r.Report.Platform)
	}
	if f := featureByKind(r, FeatureSearch); f == nil || f.Source != "built-in search" {
		t.Fatalf("search source %#v", f)
	}

	withBase := sqsDoc("Example Studio", "https://www.example.com", `<wp:base_site_url>https://example.org</wp:base_site_url><wp:base_blog_url>https://example.org</wp:base_blog_url>`, page)
	r = convertSqs(t, withBase, func(string) ([]byte, error) { return nil, nil }, "")
	if r.Report.Platform != "WordPress" {
		t.Fatalf("base urls must stay WordPress, got %q", r.Report.Platform)
	}
	if f := featureByKind(r, FeatureSearch); f == nil || f.Source != "WordPress search" {
		t.Fatalf("search source %#v", f)
	}
	if warningHas(r, "Squarespace's export leaves out") {
		t.Fatal("leaves-out warning on a WordPress export")
	}

	wpcom := sqsDoc("Example Studio", "https://example.wordpress.com", `<generator>https://wordpress.com/</generator><wp:base_site_url>https://example.wordpress.com</wp:base_site_url><wp:base_blog_url>https://example.wordpress.com</wp:base_blog_url>`, sqsItem("1", "About", "/about", "page", "<p>Hello</p>", ""))
	r = convertSqs(t, wpcom, func(string) ([]byte, error) { return nil, nil }, "")
	if r.Report.Platform != "WordPress.com" {
		t.Fatalf("platform %q", r.Report.Platform)
	}

	hosted := sqsDoc("Example Studio", "https://example.squarespace.com", "", sqsItem("1", "About", "/about", "page", "<p>Hello from Example Studio</p>", ""))
	r = convertSqs(t, hosted, func(string) ([]byte, error) { return nil, nil }, "")
	if r.Report.Platform != "Squarespace" {
		t.Fatalf("squarespace.com host: %q", r.Report.Platform)
	}

	plain, err := Convert(context.Background(), Input{Site: "moved", WXR: []io.Reader{strings.NewReader(minimalXML("<p>Hello</p>"))}})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Report.Platform != "WordPress" {
		t.Fatalf("plain WordPress export detected as %q", plain.Report.Platform)
	}
}

func TestSquarespaceCDNPhotos(t *testing.T) {
	pngBytes := tinyPNG(t)
	photo := "https://images.squarespace-cdn.com/content/v1/siteid/12345-abcdef/photo.jpg"
	old := "http://static1.squarespace.com/static/aaa/bbb/ccc/999/old.jpg"
	older := "http://static.squarespace.com/static/aaa/bbb/ddd/1000/older.jpg"
	body := sqsBlock(`<img src="` + photo + `?format=1500w" srcset="` + photo + `?format=100w 100w, ` + photo + `?format=2500w 2500w" alt="Studio">` +
		`<img src="` + old + `" alt="Older">` +
		`<img src="` + older + `" alt="Oldest">`)
	var mu sync.Mutex
	var urls []string
	r := convertSqs(t, sqsDoc("Example Studio", "https://www.example.com", "", sqsItem("1", "Studio", "/studio", "page", body, "")), func(raw string) ([]byte, error) {
		mu.Lock()
		urls = append(urls, raw)
		mu.Unlock()
		if strings.Contains(raw, "old.jpg") && strings.Contains(raw, "format=2500w") {
			return nil, errFetch
		}
		return pngBytes, nil
	}, "")
	if _, ok := r.Files["media/sqs/siteid/12345-abcdef/photo.jpg"]; !ok {
		t.Fatalf("cdn photo missing: %v", fileKeys(r))
	}
	if _, ok := r.Files["media/sqs/ccc/999/old.jpg"]; !ok {
		t.Fatalf("static1 photo missing: %v", fileKeys(r))
	}
	if _, ok := r.Files["media/sqs/ddd/1000/older.jpg"]; !ok {
		t.Fatalf("static photo missing: %v", fileKeys(r))
	}
	if r.Report.MediaCopied != 3 {
		t.Fatalf("copied %d", r.Report.MediaCopied)
	}
	page := string(r.Files["studio/index.html"])
	if strings.Contains(page, "srcset") || strings.Contains(page, "format=1500w") || !strings.Contains(page, "media/sqs/siteid/12345-abcdef/photo.jpg") {
		t.Fatalf("page images:\n%s", page)
	}
	sawSized, sawFallback, sawSmall := false, false, false
	for _, u := range urls {
		if strings.Contains(u, "photo.jpg") && strings.Contains(u, "format=2500w") {
			sawSized = true
		}
		if strings.Contains(u, "format=1500w") || strings.Contains(u, "format=100w") {
			sawSmall = true
		}
		if u == old {
			sawFallback = true
		}
	}
	if !sawSized || !sawFallback || sawSmall {
		t.Fatalf("fetches sized=%v fallback=%v small=%v %v", sawSized, sawFallback, sawSmall, urls)
	}
}

var errFetch = fetchError("unavailable")

type fetchError string

func (e fetchError) Error() string { return string(e) }

func fileKeys(r *Result) []string {
	var keys []string
	for k := range r.Files {
		keys = append(keys, k)
	}
	return keys
}

func TestSquarespaceLazyImage(t *testing.T) {
	pngBytes := tinyPNG(t)
	raw := "https://images.squarespace-cdn.com/content/v1/siteid/999-aaaa/lazy.jpg"
	body := `<div class="sqs-block image-block sqs-block-image"><img class="thumb-image" alt="Studio" data-src="` + raw + `" data-image="` + raw + `" data-image-dimensions="800x600" data-load="false" data-image-focal-point="0.5,0.5"><noscript><img src="` + raw + `" alt="Studio"></noscript></div>`
	r := convertSqs(t, sqsDoc("Example Studio", "https://www.example.com", "", sqsItem("1", "Studio", "/studio", "page", body, "")), func(string) ([]byte, error) { return pngBytes, nil }, "")
	page := string(r.Files["studio/index.html"])
	if !strings.Contains(page, "media/sqs/siteid/999-aaaa/lazy.jpg") {
		t.Fatalf("lazy image not shown:\n%s", page)
	}
	if strings.Contains(page, "noscript") || strings.Contains(page, "data-load") || strings.Contains(page, "data-image-dimensions") || strings.Contains(page, "data-image-focal-point") {
		t.Fatalf("lazy noise kept:\n%s", page)
	}
	if _, ok := r.Files["media/sqs/siteid/999-aaaa/lazy.jpg"]; !ok || r.Report.MediaCopied != 1 {
		t.Fatalf("copied %d keys %v", r.Report.MediaCopied, fileKeys(r))
	}
}

func TestSquarespaceUploadedFiles(t *testing.T) {
	body := sqsBlock(`<p><a href="/s/notes.pdf">Notes</a> <a href="/s/missing.pdf">Missing file</a> <a href="https://www.example.com/visit">Visit</a></p>`)
	doc := sqsDoc("Example Studio", "https://www.example.com", "",
		sqsItem("1", "About", "/about", "page", body, "")+
			sqsItem("2", "Visit", "/visit", "page", sqsBlock("<p>Come by Example Studio</p>"), ""))
	var mu sync.Mutex
	var urls []string
	r := convertSqs(t, doc, func(raw string) ([]byte, error) {
		mu.Lock()
		urls = append(urls, raw)
		mu.Unlock()
		if strings.Contains(raw, "missing.pdf") {
			return nil, errFetch
		}
		if strings.Contains(raw, "notes.pdf") {
			return []byte("%PDF-1.4 notes"), nil
		}
		return nil, errFetch
	}, "")
	if string(r.Files["s/notes.pdf"]) != "%PDF-1.4 notes" {
		t.Fatalf("pdf not kept at s/notes.pdf: %v", fileKeys(r))
	}
	about := string(r.Files["about/index.html"])
	if !strings.Contains(about, "../s/notes.pdf") {
		t.Fatalf("pdf link not rewritten:\n%s", about)
	}
	if strings.Contains(about, "https://www.example.com/visit") || !strings.Contains(about, ">Visit<") {
		t.Fatalf("own-host link not rewritten:\n%s", about)
	}
	found := false
	for _, u := range urls {
		if u == "https://www.example.com/s/notes.pdf" {
			found = true
		}
	}
	if !found {
		t.Fatalf("fetch urls %v", urls)
	}
	missing := false
	for _, u := range r.Report.MediaMissing {
		if u == "/s/missing.pdf" {
			missing = true
		}
	}
	if !missing {
		t.Fatalf("missing file not reported: %v", r.Report.MediaMissing)
	}
}

func TestSquarespaceSocialIcons(t *testing.T) {
	body := sqsBlock(`<p>Find Example Studio</p>
<a href="https://facebook.com/examplestudio">
<svg class="sqs-svg-icon--social" viewBox="0 0 64 64">
<use xlink:href="#facebook-icon"></use>
</svg>
</a>
<a href="mailto:hello@example.com"><svg><use xlink:href="#email-icon"></use></svg></a>
<a href="https://www.instagram.com/examplestudio"><svg class="sqs-svg-icon--social"><use href="#instagram-icon"></use></svg></a>
<svg viewBox="0 0 10 10"><path d="M0 0 L10 10"></path></svg>`)
	r := convertSqs(t, sqsDoc("Example Studio", "https://www.example.com", "", sqsItem("1", "About", "/about", "page", body, "")), func(string) ([]byte, error) { return nil, errFetch }, "")
	page := string(r.Files["about/index.html"])
	assertWellFormed(t, page)
	if strings.Contains(page, "<use") || strings.Contains(page, "<br<") || !strings.Contains(page, "<path") {
		t.Fatalf("sprite, broken tag, or lost drawing:\n%s", page)
	}
	if got := anchorText(page, "facebook.com"); got != "Facebook" {
		t.Fatalf("facebook label %q\n%s", got, page)
	}
	if got := anchorText(page, "mailto:"); got != "Email" {
		t.Fatalf("email label %q\n%s", got, page)
	}
	if got := anchorText(page, "instagram.com"); got != "Instagram" {
		t.Fatalf("instagram label %q\n%s", got, page)
	}
	if strings.Count(page, "facebook.com/examplestudio") != 1 {
		t.Fatalf("facebook link duplicated:\n%s", page)
	}
}

func anchorText(html, hrefPart string) string {
	doc, err := xhtml.Parse(strings.NewReader(html))
	if err != nil {
		return ""
	}
	var found string
	var walkFn func(*xhtml.Node)
	walkFn = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode && n.Data == "a" && strings.Contains(getAttr(n, "href"), hrefPart) {
			found = nodeText(n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walkFn(c)
		}
	}
	walkFn(doc)
	return found
}

func TestSocialLabels(t *testing.T) {
	c := testConverter()
	for _, tc := range []struct{ href, label string }{
		{"https://facebook.com/a", "Facebook"},
		{"https://www.instagram.com/a", "Instagram"},
		{"https://twitter.com/a", "X"},
		{"https://x.com/a", "X"},
		{"https://www.linkedin.com/in/a", "LinkedIn"},
		{"https://youtube.com/a", "YouTube"},
		{"mailto:hello@example.com", "Email"},
		{"https://photos.example.org/me", "photos.example.org"},
	} {
		out := c.cleanContent(`<a href="`+tc.href+`"><svg><use href="#icon"></use></svg></a>`, "/")
		if !strings.Contains(out, ">"+tc.label+"<") || strings.Contains(out, "<use") || strings.Contains(out, "<br<") {
			t.Fatalf("%s -> %s", tc.href, out)
		}
	}
}

func TestSVGWellFormed(t *testing.T) {
	c := testConverter()
	for _, in := range []string{
		`<svg><br><rect width="1" height="1"></rect></svg>`,
		"<div>\n<svg viewBox=\"0 0 10 10\">\n<rect width=\"1\" height=\"1\"></rect>\n<path d=\"M0 0 L10 10\"></path>\n</svg>\n</div>",
	} {
		out := c.cleanContent(c.renderContent(in, "/", 0), "/")
		if !strings.Contains(out, "<rect") || strings.Contains(out, "<br<") {
			t.Fatalf("input %q output %s", in, out)
		}
		assertWellFormed(t, "<!doctype html><html><body>"+out+"</body></html>")
	}
}

func TestSquarespaceForm(t *testing.T) {
	body := `<div class="sqs-block html-block sqs-block-html" data-block-css="{&quot;x&quot;:1}" data-animation-duration="2" data-sqsp-block="1"><div class="sqs-html-content"><p>Write to Example Studio</p></div></div>` +
		`<div class="sqs-block form-block sqs-block-form"><div class="sqs-block-content"></div></div>` +
		`<div class="sqs-block website-component" data-definition-name="website.components.form" data-block-scripts="[]"><div class="sqs-block-content"></div></div>`
	r := convertSqs(t, sqsDoc("Example Studio", "https://www.example.com", "", sqsItem("1", "Contact", "/contact", "page", body, "")), func(string) ([]byte, error) { return nil, errFetch }, "")
	f := featureByKind(r, FeatureContactForm)
	if f == nil {
		t.Fatal("contact feature missing")
	}
	if len(f.Setup) != 2 || f.Setup[0].Tool != "storage_set_resource" || f.Setup[1].Tool != "storage_sql_schema" {
		t.Fatalf("setup %#v", f.Setup)
	}
	const note = "Squarespace keeps a form's fields out of its export, so a name, email and message form stands in and the person can ask the chat to add the real fields."
	if f.Note != note {
		t.Fatalf("note %q", f.Note)
	}
	if strings.Contains(f.Note, "WordPress export") {
		t.Fatal(f.Note)
	}
	page := string(r.Files["contact/index.html"])
	for _, want := range []string{`name="name"`, `name="email"`, `name="message"`, "Write to Example Studio"} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing %s\n%s", want, page)
		}
	}
	if strings.Contains(page, "<h2>Squarespace</h2>") || strings.Contains(page, "data-block-css") || strings.Contains(page, "data-block-scripts") || strings.Contains(page, "data-animation-") || strings.Contains(page, "data-sqsp-") {
		t.Fatalf("form markup:\n%s", page)
	}
}

func TestSquarespaceMenuFromLiveSite(t *testing.T) {
	live := `<!doctype html><html><body>
<header><nav class="header-nav-wrapper"><a href="/skip">Skip</a></nav>
<nav id="mainNavigation" class="main-nav">
<div class="header-nav-item"><a href="/about">About</a></div>
<div class="header-nav-item"><a href="/work">Work</a>
<div class="header-nav-folder"><div class="header-nav-folder-item"><a href="/work/paintings">Paintings</a></div></div>
</div>
<div class="header-nav-item"><a href="/missing">Missing</a></div>
<div class="header-nav-item"><a href="/blog">Journal</a></div>
</nav></header></body></html>`
	var items string
	items += sqsItem("1", "About", "/about", "page", sqsBlock("<p>About Example Studio</p>"), "")
	items += sqsItem("2", "Work", "/work", "page", sqsBlock("<p>Work</p>"), "")
	items += sqsItem("3", "Paintings", "/work/paintings", "page", sqsBlock("<p>Paintings</p>"), "")
	for n := 1; n <= 9; n++ {
		items += sqsItem("s"+string(rune('0'+n)), "Spare", "/spare-"+string(rune('0'+n)), "page", sqsBlock("<p>Spare page</p>"), "")
	}
	items += sqsItem("20", "A note", "/blog/2014/2/11/note", "post", sqsBlock("<p>A note from Example Studio</p>"), "")
	var fetched []string
	r := convertSqs(t, sqsDoc("Example Studio", "https://www.example.com", "", items), func(raw string) ([]byte, error) {
		fetched = append(fetched, raw)
		if raw == "https://www.example.com/" {
			return []byte(live), nil
		}
		return nil, errFetch
	}, "https://www.example.com/")
	wide := navWide(string(r.Files["index.html"]))
	for _, want := range []string{">About<", ">Work<", ">Paintings<", ">Journal<"} {
		if !strings.Contains(wide, want) {
			t.Fatalf("menu missing %s: %s", want, wide)
		}
	}
	if strings.Contains(wide, "Missing") || strings.Contains(wide, "Spare") {
		t.Fatalf("menu kept extras: %s", wide)
	}
	work := strings.Index(wide, ">Work<")
	details := strings.Index(wide, "<details>")
	paint := strings.Index(wide, ">Paintings<")
	if details < 0 || work < details || paint < work {
		t.Fatalf("folder item not nested: %s", wide)
	}
	if warningHas(r, "does not include the site's menu") {
		t.Fatalf("menu warning despite a live menu: %v", r.Report.Warnings)
	}
	if len(fetched) == 0 || fetched[0] != "https://www.example.com/" {
		t.Fatalf("theme page not fetched: %v", fetched)
	}
}

func TestSquarespaceMenuFallback(t *testing.T) {
	t.Run("few pages", func(t *testing.T) {
		items := sqsItem("1", "About", "/about", "page", sqsBlock("<p>About</p>"), "") +
			sqsItem("2", "Visit", "/visit", "page", sqsBlock("<p>Visit</p>"), "") +
			sqsItem("3", "Contact", "/contact", "page", sqsBlock("<p>Contact</p>"), "")
		r := convertSqs(t, sqsDoc("Example Studio", "https://www.example.com", "", items), func(string) ([]byte, error) { return nil, errFetch }, "")
		wide := navWide(string(r.Files["index.html"]))
		for _, title := range []string{">About<", ">Visit<", ">Contact<"} {
			if !strings.Contains(wide, title) {
				t.Fatalf("flat menu missing %s: %s", title, wide)
			}
		}
		if warningHas(r, "does not include the site's menu") {
			t.Fatal(r.Report.Warnings)
		}
	})
	t.Run("many pages", func(t *testing.T) {
		var items string
		for n := 1; n <= 9; n++ {
			name := "P" + string(rune('0'+n))
			items += sqsItem(name, name, "/"+strings.ToLower(name), "page", sqsBlock("<p>"+name+"</p>"), "")
		}
		r := convertSqs(t, sqsDoc("Example Studio", "https://www.example.com", "", items), func(string) ([]byte, error) { return nil, errFetch }, "")
		wide := navWide(string(r.Files["index.html"]))
		if strings.Contains(wide, ">P1<") || strings.Contains(wide, ">P9<") {
			t.Fatalf("page links in a long menu: %s", wide)
		}
		index := string(r.Files["index.html"])
		if !strings.Contains(index, ">Search<") {
			t.Fatal("search link missing")
		}
		const want = "Your Squarespace export does not include the site's menu, so the header has no menu yet. Fill in the old address when importing to copy it, or ask the chat to add a menu with the pages you want."
		if !warningIs(r, want) {
			t.Fatalf("warnings %v", r.Report.Warnings)
		}
	})
	t.Run("duplicate titles", func(t *testing.T) {
		items := sqsItem("1", "Home", "/home", "page", sqsBlock("<p>First home</p>"), "") +
			sqsItem("2", "Home", "/welcome", "page", sqsBlock("<p>Second home</p>"), "")
		r := convertSqs(t, sqsDoc("Example Studio", "https://www.example.com", "", items), func(string) ([]byte, error) { return nil, errFetch }, "")
		if navWide(string(r.Files["index.html"])) != "" {
			t.Fatalf("duplicate titles still linked: %s", navWide(string(r.Files["index.html"])))
		}
		if !strings.Contains(string(r.Files["index.html"]), ">Search<") {
			t.Fatal("search link missing")
		}
		if !warningHas(r, "does not include the site's menu") {
			t.Fatal(r.Report.Warnings)
		}
	})
	t.Run("wordpress keeps the flat list", func(t *testing.T) {
		var items string
		for n := 1; n <= 9; n++ {
			name := "W" + string(rune('0'+n))
			items += sqsItem(name, name, "https://example.org/"+strings.ToLower(name)+"/", "page", "<p>Hello</p>", "")
		}
		doc := sqsDoc("Example", "https://example.org", "", items)
		r := convertSqs(t, doc, func(string) ([]byte, error) { return nil, errFetch }, "")
		if r.Report.Platform != "WordPress" {
			t.Fatalf("platform %q", r.Report.Platform)
		}
		wide := navWide(string(r.Files["index.html"]))
		if !strings.Contains(wide, ">W1<") || !strings.Contains(wide, ">W9<") {
			t.Fatalf("wordpress menu dropped pages: %s", wide)
		}
		if warningHas(r, "Squarespace") {
			t.Fatalf("squarespace warning on wordpress: %v", r.Report.Warnings)
		}
	})
}

func TestSquarespaceNoThemeCopy(t *testing.T) {
	live := `<!doctype html><html><head><link rel="stylesheet" href="https://www.example.com/site.css"></head><body class="page-id-2"><p>Live Example Studio</p></body></html>`
	cssAsked := false
	r := convertSqs(t, sqsDoc("Example Studio", "https://www.example.com", "", sqsItem("2", "About", "/about", "page", sqsBlock("<p>About</p>"), "")), func(raw string) ([]byte, error) {
		if strings.Contains(raw, "site.css") {
			cssAsked = true
			return []byte("body{color:red}"), nil
		}
		if raw == "https://www.example.com/" {
			return []byte(live), nil
		}
		return nil, errFetch
	}, "https://www.example.com/")
	if r.Report.ThemeCopied {
		t.Fatal("theme was copied")
	}
	if cssAsked {
		t.Fatal("stylesheet was fetched")
	}
	if r.Report.FrontPage != "page:/about/" {
		t.Fatalf("front page %q", r.Report.FrontPage)
	}
	if _, ok := r.Files["sh-import/theme.css"]; !ok {
		t.Fatal("built-in theme missing")
	}
	for k := range r.Files {
		if strings.HasPrefix(k, "sh-import/theme/") {
			t.Fatal("copied theme file", k)
		}
	}
	if !warningIs(r, "Squarespace's design (fonts, colours, layout) does not come over, so the site uses a plain built-in look. Ask the chat to restyle it.") {
		t.Fatalf("warnings %v", r.Report.Warnings)
	}
	if warningHas(r, "Theme copy was incomplete") || warningHas(r, "Theme page could not be fetched") {
		t.Fatalf("theme warnings %v", r.Report.Warnings)
	}

	failed := convertSqs(t, sqsDoc("Example Studio", "https://www.example.com", "", sqsItem("2", "About", "/about", "page", sqsBlock("<p>About</p>"), "")), func(string) ([]byte, error) {
		return nil, errFetch
	}, "https://www.example.com/missing")
	if failed.Report.ThemeCopied || warningHas(failed, "Theme page could not be fetched") || warningHas(failed, "Theme copy was incomplete") {
		t.Fatalf("failed fetch copied or warned: copied=%v %v", failed.Report.ThemeCopied, failed.Report.Warnings)
	}
}

func TestSquarespacePlaceholderTitle(t *testing.T) {
	for _, title := range []string{"Your Site Title", ""} {
		r := convertSqs(t, sqsDoc(title, "https://www.example.com", "", sqsItem("1", "About", "/about", "page", sqsBlock("<p>About</p>"), "")), func(string) ([]byte, error) { return nil, errFetch }, "")
		if r.Report.SiteTitle != "example.com" {
			t.Fatalf("title %q -> %q", title, r.Report.SiteTitle)
		}
		if !strings.Contains(string(r.Files["index.html"]), ">example.com<") {
			t.Fatalf("header title missing for %q", title)
		}
	}
	r := convertSqs(t, sqsDoc("Example Studio", "https://www.example.com", "", sqsItem("1", "About", "/about", "page", sqsBlock("<p>About</p>"), "")), func(string) ([]byte, error) { return nil, errFetch }, "")
	if r.Report.SiteTitle != "Example Studio" {
		t.Fatalf("real title replaced: %q", r.Report.SiteTitle)
	}
}

func TestSquarespaceReportWarnings(t *testing.T) {
	comment := `<wp:comment_status>open</wp:comment_status><wp:comment><wp:comment_id>10</wp:comment_id><wp:comment_author>Ada</wp:comment_author><wp:comment_date>2014-02-12 09:00:00</wp:comment_date><wp:comment_content>Nice work at Example Studio.</wp:comment_content><wp:comment_approved>1</wp:comment_approved></wp:comment>`
	items := sqsItem("1", "Home", "/home", "page", `<div class="fe-block"><p>Welcome to Example Studio</p></div>`, "") +
		sqsItem("2", "A note", "/blog/2014/2/11/note", "post", sqsBlock("<p>A note</p>"), comment)
	r := convertSqs(t, sqsDoc("Example Studio", "https://www.example.com", "", items), func(string) ([]byte, error) { return nil, errFetch }, "")
	const leaves = "Squarespace's export leaves out store, event, portfolio, album and index pages, every blog except one, audio, video and product blocks, and form fields. Pages missing here can be added by asking the chat; products can come from Squarespace's product CSV export (Commerce > Inventory > Export)."
	const layout = "1 page used Squarespace's page layout. Its words and photos came over in one column; ask the chat to adjust anything."
	if !warningIs(r, leaves) || !warningIs(r, layout) {
		t.Fatalf("warnings %v", r.Report.Warnings)
	}
	if f := featureByKind(r, FeatureComments); f == nil || f.Source != "Squarespace comments" {
		t.Fatalf("comments %#v", f)
	}
	if f := featureByKind(r, FeatureSearch); f == nil || f.Source != "built-in search" {
		t.Fatalf("search %#v", f)
	}

	two := sqsItem("1", "Home", "/home", "page", `<div class="sqs-block fluid-engine"><p>One</p></div>`, "") +
		sqsItem("2", "Work", "/work", "page", `<div class="sqs-block sqs-row"><div class="sqs-col"><p>Left</p></div><div class="sqs-col"><p>Right</p></div></div>`, "")
	r = convertSqs(t, sqsDoc("Example Studio", "https://www.example.com", "", two), func(string) ([]byte, error) { return nil, errFetch }, "")
	if !warningIs(r, "2 pages used Squarespace's page layout. Their words and photos came over in one column; ask the chat to adjust anything.") {
		t.Fatalf("warnings %v", r.Report.Warnings)
	}

	oneCol := sqsItem("1", "Home", "/home", "page", sqsBlock(`<div class="sqs-row"><div class="sqs-col"><p>Only one</p></div></div>`), "")
	r = convertSqs(t, sqsDoc("Example Studio", "https://www.example.com", "", oneCol), func(string) ([]byte, error) { return nil, errFetch }, "")
	if warningHas(r, "page layout") {
		t.Fatalf("one column counted as a layout: %v", r.Report.Warnings)
	}

	wp := sqsDoc("Example", "https://example.org", `<wp:base_site_url>https://example.org</wp:base_site_url><wp:base_blog_url>https://example.org</wp:base_blog_url>`, sqsItem("1", "About", "/about", "page", "<p>Hello</p>", ""))
	r = convertSqs(t, wp, func(string) ([]byte, error) { return nil, errFetch }, "")
	if warningHas(r, "Squarespace") {
		t.Fatalf("squarespace warning on wordpress: %v", r.Report.Warnings)
	}
}

func TestSquarespaceMissingPhotoNote(t *testing.T) {
	x := `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:wp="http://wordpress.org/export/1.2/"><channel><title>Example Studio</title><link>https://www.example.com</link><wp:wxr_version>1.2</wp:wxr_version>` +
		`<item><title>About</title><link>/about</link><content:encoded><![CDATA[<div class="sqs-block image-block"><img src="https://images.squarespace-cdn.com/content/v1/abc/123-XYZ/studio.jpg?format=1500w"></div>]]></content:encoded><wp:post_id>1</wp:post_id><wp:post_name>about</wp:post_name><wp:status>publish</wp:status><wp:post_type>page</wp:post_type></item></channel></rss>`
	res, err := Convert(context.Background(), Input{Site: "s", WXR: []io.Reader{strings.NewReader(x)}, Fetch: func(context.Context, string) ([]byte, error) { return nil, errors.New("gone") }})
	if err != nil {
		t.Fatal(err)
	}
	w := strings.Join(res.Report.Warnings, "\n")
	if !strings.Contains(w, "for example studio.jpg") || strings.Contains(w, "media archive") || !strings.Contains(w, "Keep the Squarespace site live") {
		t.Fatalf("warnings: %s", w)
	}
}
