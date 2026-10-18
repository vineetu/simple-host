package wpimport

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

func convertWXR(t *testing.T, wxr string, media map[string]MediaFile, fetch func(context.Context, string) ([]byte, error)) *Result {
	t.Helper()
	if media == nil {
		media = map[string]MediaFile{}
	}
	r, err := Convert(context.Background(), Input{Site: "example-bakery", WXR: []io.Reader{strings.NewReader(wxr)}, Media: media, Fetch: fetch})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// bakeryChannel is a tiny invented WXR. lang "OMIT" leaves the channel
// language out; any other value is written as the language element, including
// an empty one.
func bakeryChannel(lang, link, base, blog, extra string) string {
	if link == "" {
		link = "https://example-bakery.test"
	}
	if base == "" {
		base = link
	}
	if blog == "" {
		blog = link
	}
	langXML := "<language>" + lang + "</language>\n"
	if lang == "OMIT" {
		langXML = ""
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<rss xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:wp="http://wordpress.org/export/1.2/">
<channel>
<title>Example Bakery</title>
<link>` + link + `</link>
<description>Bread and buns</description>
` + langXML + `<wp:base_site_url>` + base + `</wp:base_site_url>
<wp:base_blog_url>` + blog + `</wp:base_blog_url>
` + extra + `
</channel>
</rss>`
}

func bakeryPage(id, title, path, kind, content string) string {
	if kind == "" {
		kind = "page"
	}
	return `<item><title>` + title + `</title><link>https://example-bakery.test` + path + `</link><dc:creator>baker</dc:creator><wp:post_id>` + id + `</wp:post_id><wp:post_name>item-` + id + `</wp:post_name><wp:status>publish</wp:status><wp:post_type>` + kind + `</wp:post_type><content:encoded><![CDATA[` + content + `]]></content:encoded></item>`
}

func TestRealExportPageLanguage(t *testing.T) {
	home := bakeryPage("1", "Home", "/", "page", "<p>Rye loaves.</p>")
	for _, lang := range []string{"zh-CN", "es", "cs", "sv-SE", "en-US"} {
		t.Run(lang, func(t *testing.T) {
			r := convertWXR(t, bakeryChannel(lang, "", "", "", home), nil, nil)
			page := string(r.Files["index.html"])
			if !strings.Contains(page, `<html lang="`+lang+`">`) || strings.Contains(page, " dir=") {
				t.Fatalf("language tag: %s", page[:strings.Index(page, "<head>")])
			}
			search := string(r.Files["search/index.html"])
			if !strings.Contains(page, ">Menu</summary>") || !strings.Contains(page, ">Search</a>") || !strings.Contains(search, ">Search</button>") {
				t.Fatal("fixed words changed", page)
			}
			if !strings.Contains(search, `<html lang="`+lang+`">`) {
				t.Fatal("search language", search[:strings.Index(search, "<head>")])
			}
		})
	}
	for _, lang := range []string{"ar", "he", "iw", "fa", "ur", "ps", "sd", "ug", "yi", "dv", "ckb", "ar-SA", "he-IL"} {
		t.Run(lang, func(t *testing.T) {
			r := convertWXR(t, bakeryChannel(lang, "", "", "", home), nil, nil)
			page := string(r.Files["index.html"])
			if !strings.Contains(page, `<html lang="`+lang+`" dir="rtl">`) {
				t.Fatalf("rtl tag: %s", page[:strings.Index(page, "<head>")])
			}
			if !strings.Contains(page, ">Menu</summary>") || !strings.Contains(page, ">Search</a>") {
				t.Fatal("fixed words changed")
			}
		})
	}
	for _, lang := range []string{"", "!!!", "x", "en_US", "not-a-language", strings.Repeat("a", 40)} {
		t.Run(fmt.Sprintf("invalid-%q", lang), func(t *testing.T) {
			r := convertWXR(t, bakeryChannel(lang, "", "", "", home), nil, nil)
			page := string(r.Files["index.html"])
			if !strings.Contains(page, `<html lang="en">`) || strings.Contains(page, " dir=") {
				t.Fatalf("fallback tag: %s", page[:strings.Index(page, "<head>")])
			}
		})
	}
	t.Run("sanitised", func(t *testing.T) {
		for _, lang := range []string{" zh-CN ", "zh-CN!"} {
			r := convertWXR(t, bakeryChannel(lang, "", "", "", home), nil, nil)
			page := string(r.Files["index.html"])
			if !strings.Contains(page, `<html lang="zh-CN">`) || strings.Contains(page, " dir=") {
				t.Fatalf("sanitised %q: %s", lang, page[:strings.Index(page, "<head>")])
			}
		}
	})
	t.Run("polylang", func(t *testing.T) {
		extra := `<item><title>Home</title><link>https://example-bakery.test/</link><dc:creator>baker</dc:creator><wp:post_id>1</wp:post_id><wp:status>publish</wp:status><wp:post_type>page</wp:post_type><category domain="language" nicename="en"><![CDATA[English]]></category><content:encoded><![CDATA[<p>Rye loaves.</p>]]></content:encoded></item>
<item><title>Arabic</title><link>https://example-bakery.test/ar/</link><dc:creator>baker</dc:creator><wp:post_id>2</wp:post_id><wp:status>publish</wp:status><wp:post_type>page</wp:post_type><category domain="language" nicename="ar"><![CDATA[Arabic]]></category><content:encoded><![CDATA[<p>Flat loaves.</p>]]></content:encoded></item>
<item><title>Hours</title><link>https://example-bakery.test/hours/</link><dc:creator>baker</dc:creator><wp:post_id>3</wp:post_id><wp:status>publish</wp:status><wp:post_type>page</wp:post_type><content:encoded><![CDATA[<p>Open at dawn.</p>]]></content:encoded></item>`
		r := convertWXR(t, bakeryChannel("fr", "", "", "", extra), nil, nil)
		homePage := string(r.Files["index.html"])
		ar := string(r.Files["ar/index.html"])
		hours := string(r.Files["hours/index.html"])
		search := string(r.Files["search/index.html"])
		if !strings.Contains(homePage, `<html lang="en">`) || strings.Contains(homePage, `lang="fr"`) || strings.Contains(homePage, " dir=") {
			t.Fatal("home language", homePage[:strings.Index(homePage, "<head>")])
		}
		if !strings.Contains(ar, `<html lang="ar" dir="rtl">`) {
			t.Fatal("arabic language", ar[:strings.Index(ar, "<head>")])
		}
		for _, page := range []string{hours, search} {
			if !strings.Contains(page, `<html lang="en">`) || strings.Contains(page, `lang="fr"`) || strings.Contains(page, " dir=") {
				t.Fatal("default language", page[:strings.Index(page, "<head>")])
			}
		}
		if !strings.Contains(ar, ">Menu</summary>") || !strings.Contains(search, ">Search</a>") {
			t.Fatal("fixed words changed")
		}
	})
}

func TestRealExportMediaDownloadWarning(t *testing.T) {
	body := `<p><img src="https://example-bakery.test/wp-content/uploads/2016/10/a.jpg"><img src="https://example-bakery.test/wp-content/uploads/2016/10/b.jpg"><img src="/2016/10/c.jpg"><img src="/2024/01/loaf.jpg"></p>
<p><a href="/2024/01/15/my-post/">A story</a> <a href="https://example-bakery.test/2024/01/15/my-post/">Same story</a></p>`
	var mu sync.Mutex
	var fetched []string
	r := convertWXR(t, bakeryChannel("en", "", "", "", bakeryPage("1", "Home", "/", "page", body)), nil, func(_ context.Context, u string) ([]byte, error) {
		mu.Lock()
		fetched = append(fetched, u)
		mu.Unlock()
		return nil, fmt.Errorf("gone")
	})
	n := 0
	var got string
	for _, w := range r.Report.Warnings {
		if strings.Contains(w, "could not be downloaded from the old site") {
			n++
			got = w
		}
	}
	if n != 1 || strings.Contains(got, "\n") {
		t.Fatalf("warnings: %v", r.Report.Warnings)
	}
	for _, want := range []string{"4 photos or files", "/2016/10/a.jpg", "will break when it goes offline", "Upload your media archive with the export"} {
		if !strings.Contains(got, want) {
			t.Fatalf("warning %q missing %q", got, want)
		}
	}
	page := string(r.Files["index.html"])
	if !strings.Contains(page, `src="https://example-bakery.test/wp-content/uploads/2016/10/a.jpg"`) {
		t.Fatal("failed photo no longer points at the old address", page)
	}
	for _, u := range fetched {
		if strings.Contains(u, "my-post") {
			t.Fatal("post permalink fetched as media", fetched)
		}
	}
	joined := strings.Join(fetched, "\n")
	if !strings.Contains(joined, "/2024/01/loaf.jpg") || !strings.Contains(joined, "2016/10/a.jpg") {
		t.Fatal("real files were not fetched", fetched)
	}
	for p := range r.Files {
		if strings.Contains(p, "my-post") {
			t.Fatal("permalink stored as a file", p)
		}
	}
	missing := strings.Join(r.Report.MediaMissing, "\n")
	if !strings.Contains(missing, "2016/10/a.jpg") || strings.Contains(missing, "my-post") {
		t.Fatal("media list", r.Report.MediaMissing)
	}
}

func TestRealExportPageIDLinks(t *testing.T) {
	extra := bakeryPage("1", "Home", "/", "page", `<p><a href="https://example-bakery.test/?page_id=15">About the bakery</a></p><p><a href="https://example-bakery.test/?p=16">Rye story</a></p><p><a href="https://example-bakery.test/?p=99">Secret draft</a></p>`) +
		bakeryPage("15", "About", "/about/", "page", "<p>About the bakery.</p>") +
		bakeryPage("16", "Rye story", "/2024/03/rye/", "post", "<p>The rye story.</p>") +
		`<item><title>Secret draft</title><link>https://example-bakery.test/?p=99</link><wp:post_id>99</wp:post_id><wp:status>draft</wp:status><wp:post_type>page</wp:post_type><content:encoded><![CDATA[<p>Hidden recipe.</p>]]></content:encoded></item>`
	r := convertWXR(t, bakeryChannel("en", "", "", "", extra), nil, nil)
	page := string(r.Files["index.html"])
	if !strings.Contains(page, `href="about/"`) || !strings.Contains(page, `href="2024/03/rye/"`) {
		t.Fatal("public id links were not rewritten", page)
	}
	if strings.Contains(page, "page_id=") || strings.Contains(page, "?p=16") || strings.Contains(page, "/p/15/") || strings.Contains(page, "/p/16/") {
		t.Fatal("synthetic id path kept", page)
	}
	if !strings.Contains(page, `href="https://example-bakery.test/?p=99"`) {
		t.Fatal("draft link changed", page)
	}
	if r.Files["about/index.html"] == nil || r.Files["2024/03/rye/index.html"] == nil {
		t.Fatal("targets missing", r.Files)
	}
}

func TestRealExportWordPressComAdminLinks(t *testing.T) {
	link := "https://examplebakery.wordpress.com"
	extra := `<item><title>Home</title><link>https://examplebakery.wordpress.com/</link><dc:creator>baker</dc:creator><wp:post_id>1</wp:post_id><wp:status>publish</wp:status><wp:post_type>page</wp:post_type><content:encoded><![CDATA[<p><a href="https://wordpress.com/page/235903881/new/">Create a page</a></p><p><a href="https://www.wordpress.com/start/">Start a site</a></p><p><a href="https://examplebakery.wordpress.com/about/">Our story</a></p>]]></content:encoded></item>
<item><title>About</title><link>https://examplebakery.wordpress.com/about/</link><dc:creator>baker</dc:creator><wp:post_id>2</wp:post_id><wp:status>publish</wp:status><wp:post_type>page</wp:post_type><content:encoded><![CDATA[<p>Our story.</p>]]></content:encoded></item>`
	r := convertWXR(t, bakeryChannel("en", link, "http://wordpress.com/", link, extra), nil, nil)
	page := string(r.Files["index.html"])
	if !strings.Contains(page, "Create a page") || !strings.Contains(page, "Start a site") || !strings.Contains(page, "Our story") {
		t.Fatal("link text lost", page)
	}
	if !strings.Contains(page, `href="about/"`) {
		t.Fatal("blog link not rewritten", page)
	}
	for name, b := range r.Files {
		s := string(b)
		if strings.Contains(s, "235903881") || strings.Contains(s, "www.wordpress.com") || strings.Contains(s, "https://wordpress.com") {
			t.Fatal("platform link kept", name, s)
		}
	}
}

func TestRealExportSocialLinks(t *testing.T) {
	body := `<!-- wp:social-links -->
<ul class="wp-block-social-links"><!-- wp:social-link {"url":"https://facebook.com/examplebakery","service":"facebook"} /-->
<!-- wp:social-link {"url":"https://x.com/examplebakery","service":"x"} /-->
<!-- wp:social-link {"url":"https://example-bakery.test/about/","service":"mail","label":"Write to us"} /-->
<!-- wp:social-link {"url":"https://example-bakery.test/","service":"mail"} /-->
<!-- wp:social-link {"url":"https://notes.example/bakery","service":"floof"} /-->
<!-- wp:social-link {"service":"facebook"} /-->
<!-- wp:social-link {"url":"javascript:alert(1)","service":"facebook"} /-->
<!-- wp:social-link {"url":"https://example-bakery.test/about/","service":"chain"} /-->
</ul>
<!-- /wp:social-links -->`
	extra := bakeryPage("1", "Home", "/", "page", body) + bakeryPage("2", "About", "/about/", "page", "<p>About the bakery.</p>")
	r := convertWXR(t, bakeryChannel("en", "", "", "", extra), nil, nil)
	page := string(r.Files["index.html"])
	for _, want := range []string{
		`class="wp-block-social-links"`,
		`href="https://facebook.com/examplebakery"`, `>Facebook</a>`,
		`href="https://x.com/examplebakery"`, `>X</a>`,
		`href="about/"`, `>Write to us</a>`,
		`href="./"`, `>Email</a>`,
		`href="https://notes.example/bakery"`, `>notes.example</a>`,
		`>Link</a>`,
		`rel="noopener"`,
	} {
		if !strings.Contains(page, want) {
			t.Fatal("missing", want, page)
		}
	}
	if strings.Count(page, `rel="noopener"`) != 6 {
		t.Fatal("link count", strings.Count(page, `rel="noopener"`), page)
	}
	if strings.Contains(page, "javascript:") || strings.Count(page, ">Facebook</a>") != 1 {
		t.Fatal("unsafe or empty social link kept", page)
	}
}

func TestRealExportVideoShortcodeURL(t *testing.T) {
	body := `[video url="https://www.youtube.com/watch?v=ryeloaf01"]
[video url="https://vimeo.com/123456789"]
[video]https://www.youtube.com/watch?v=ryeloaf02[/video]
[video]
[audio]
[video src=""]
[video src="https://example-bakery.test/wp-content/uploads/2024/01/tour.mp4"]`
	r := convertWXR(t, bakeryChannel("en", "", "", "", bakeryPage("1", "Home", "/", "page", body)), nil, nil)
	page := string(r.Files["index.html"])
	for _, want := range []string{
		"https://www.youtube-nocookie.com/embed/ryeloaf01",
		"https://www.youtube-nocookie.com/embed/ryeloaf02",
		"https://player.vimeo.com/video/123456789",
		`src="https://example-bakery.test/wp-content/uploads/2024/01/tour.mp4"`,
	} {
		if !strings.Contains(page, want) {
			t.Fatal("missing", want, page)
		}
	}
	if strings.Contains(page, `src=""`) || !strings.Contains(page, "<video controls") || strings.Contains(page, "<audio") {
		t.Fatal("empty media element", page)
	}
	if r.Report.Dropped["shortcode:video"] < 2 || r.Report.Dropped["shortcode:audio"] < 1 {
		t.Fatal("dropped", r.Report.Dropped)
	}
}

func TestRealExportDiviContent(t *testing.T) {
	body := `[et_pb_section background_image="https://cdn.example-bakery.test/WALLPAPER-NOT-CONTENT.jpg"][et_pb_row][et_pb_column type="1_2"]
[et_pb_text]<p>Rye loaves every morning.</p>[/et_pb_text]
[et_pb_image src="https://example-bakery.test/wp-content/uploads/2026/07/photo.png" alt="Rye loaf" title_text="Morning rye" url="https://example-bakery.test/order/"][/et_pb_image]
[et_pb_gallery gallery_ids="12,13"][/et_pb_gallery]
[et_pb_blurb title="Fresh bread" url="https://example-bakery.test/bread/" image="https://example-bakery.test/wp-content/uploads/2026/07/second.jpg"]Baked before dawn.[/et_pb_blurb]
[et_pb_button button_url="https://example-bakery.test/order/" button_text="Order a loaf"][/et_pb_button]
[et_pb_number_counter number="8" title="Years of baking" percent_sign="on"][/et_pb_number_counter]
[et_pb_social_media_follow_network social_network="instagram" url="https://instagram.com/examplebakery"][/et_pb_social_media_follow_network]
[et_pb_fullwidth_image src="https://example-bakery.test/wp-content/uploads/2026/07/photo.png" alt="Storefront"][/et_pb_fullwidth_image]
[et_pb_fullwidth_header title="Welcome in" subhead="Baked this morning" logo_image_url="https://example-bakery.test/wp-content/uploads/2026/07/second.jpg" header_image_url="https://example-bakery.test/wp-content/uploads/2026/07/photo.png" button_one_text="Visit us" button_one_url="https://example-bakery.test/visit/"][/et_pb_fullwidth_header]
[/et_pb_column][/et_pb_row][/et_pb_section]`
	extra := bakeryPage("1", "Home", "/", "page", body) +
		bakeryPage("2", "Order", "/order/", "page", "<p>Order a loaf.</p>") +
		bakeryPage("3", "Bread", "/bread/", "page", "<p>Fresh bread.</p>") +
		bakeryPage("4", "Visit", "/visit/", "page", "<p>Visit us.</p>") +
		`<item><title>Loaf</title><wp:post_id>12</wp:post_id><wp:post_type>attachment</wp:post_type><wp:status>inherit</wp:status><wp:attachment_url>https://example-bakery.test/wp-content/uploads/2026/07/photo.png</wp:attachment_url></item>` +
		`<item><title>Bun</title><wp:post_id>13</wp:post_id><wp:post_type>attachment</wp:post_type><wp:status>inherit</wp:status><wp:attachment_url>https://example-bakery.test/wp-content/uploads/2026/07/second.jpg</wp:attachment_url></item>`
	r := convertWXR(t, bakeryChannel("en", "", "", "", extra), testMedia(), nil)
	page := string(r.Files["index.html"])
	for _, want := range []string{
		"Rye loaves every morning.",
		"Baked before dawn.",
		"Welcome in",
		"Baked this morning",
		`alt="Rye loaf"`,
		`title="Morning rye"`,
		`alt="Storefront"`,
		`alt="Fresh bread"`,
		`alt="Loaf"`,
		`alt="Bun"`,
		"wp-content/uploads/2026/07/photo.png",
		"wp-content/uploads/2026/07/second.jpg",
		`class="gallery gallery-size-"`,
		`class="wp-block-button__link"`,
		"Order a loaf",
		"Visit us",
		`href="order/"`,
		`href="visit/"`,
		`href="bread/"`,
		"<strong>8%</strong>",
		"Years of baking",
		`href="https://instagram.com/examplebakery"`,
		">Instagram</a>",
	} {
		if !strings.Contains(page, want) {
			t.Fatal("missing", want, page)
		}
	}
	if strings.Contains(page, "[et_pb_") || strings.Contains(page, "WALLPAPER-NOT-CONTENT") || strings.Contains(page, "et_pb_column") {
		t.Fatal("builder markup kept", page)
	}
	for _, tag := range []string{"et_pb_image", "et_pb_gallery", "et_pb_button", "et_pb_blurb", "et_pb_number_counter", "et_pb_fullwidth_header"} {
		if r.Report.Dropped["shortcode:"+tag] != 0 {
			t.Fatal("mapped shortcode dropped", tag, r.Report.Dropped)
		}
	}
	if r.Files["wp-content/uploads/2026/07/photo.png"] == nil || r.Files["wp-content/uploads/2026/07/second.jpg"] == nil {
		t.Fatal("photos were not copied", r.Report)
	}
	for name, b := range r.Files {
		if strings.Contains(string(b), "WALLPAPER-NOT-CONTENT") || strings.Contains(name, "WALLPAPER") {
			t.Fatal("background image copied", name)
		}
	}
	if strings.Contains(strings.Join(r.Report.MediaMissing, "\n"), "WALLPAPER") {
		t.Fatal("background listed as missing", r.Report.MediaMissing)
	}
	found := false
	for _, w := range r.Report.Warnings {
		if strings.Contains(w, "Divi") && strings.Contains(w, "words, photos and buttons") && strings.Contains(w, "one-column layout") {
			found = true
		}
	}
	if !found {
		t.Fatal("divi warning", r.Report.Warnings)
	}
}

func TestRealExportHyphenPluginShortcodes(t *testing.T) {
	body := `[theme-my-login]
<p>Please sign in to order. Weather: [powr-weather id=1]. Range [1-2] of loaves. [sic] as printed. [Note: the rye is new]. See [text](https://example-bakery.test/menu/) and [user_id] and [new_plugin] and [new_plugin id=2]. Before [theme-my-login]hello bakery[/theme-my-login] after.</p>
<pre>[theme-my-login] [powr-weather id=1]</pre>
<code class="note">[new_plugin id=2]</code>
<CODE>[keep-this]</CODE>`
	r := convertWXR(t, bakeryChannel("en", "", "", "", bakeryPage("1", "Home", "/", "page", body)), nil, nil)
	page := string(r.Files["index.html"])
	bodyOut := page[strings.Index(page, `class="entry-content"`):]
	if strings.Count(bodyOut, "[theme-my-login]") != 1 || strings.Count(bodyOut, "[powr-weather id=1]") != 1 || strings.Count(bodyOut, "[new_plugin id=2]") != 1 || strings.Count(bodyOut, "[keep-this]") != 1 {
		t.Fatal("shortcode literals", bodyOut)
	}
	for _, want := range []string{"[1-2]", "[sic]", "[Note: the rye is new]", "[text](https://example-bakery.test/menu/)", "[user_id]", "[new_plugin]", "hello bakery"} {
		if !strings.Contains(page, want) {
			t.Fatal("lost", want, page)
		}
	}
	if strings.Count(bodyOut, "[new_plugin]") != 1 {
		t.Fatal("bare underscore literal", strings.Count(bodyOut, "[new_plugin]"), bodyOut)
	}
	if r.Report.Dropped["shortcode:theme-my-login"] < 1 || r.Report.Dropped["shortcode:powr-weather"] < 1 || r.Report.Dropped["shortcode:new_plugin"] < 1 {
		t.Fatal("not counted", r.Report.Dropped)
	}
	if r.Report.Dropped["shortcode:user_id"] != 0 || r.Report.Dropped["shortcode:keep-this"] != 0 {
		t.Fatal("literal counted as a shortcode", r.Report.Dropped)
	}
}

func TestRealExportAuthorEmail(t *testing.T) {
	items := func(display string) string {
		return `<wp:author><wp:author_login>ann@example.com</wp:author_login><wp:author_nicename></wp:author_nicename><wp:author_display_name>` + display + `</wp:author_display_name></wp:author>
<wp:author><wp:author_login>sam</wp:author_login><wp:author_nicename></wp:author_nicename><wp:author_display_name>Sam Cole</wp:author_display_name></wp:author>
<item><title>Rye morning</title><link>https://example-bakery.test/rye-morning/</link><dc:creator>ann@example.com</dc:creator><wp:post_id>1</wp:post_id><wp:status>publish</wp:status><wp:post_type>post</wp:post_type><content:encoded><![CDATA[<p>The rye rose well.</p>]]></content:encoded></item>
<item><title>Buns on Friday</title><link>https://example-bakery.test/buns-on-friday/</link><dc:creator>sam</dc:creator><wp:post_id>2</wp:post_id><wp:status>publish</wp:status><wp:post_type>post</wp:post_type><content:encoded><![CDATA[<p>Buns come out at noon.</p>]]></content:encoded></item>
<item><title>Baking questions</title><link>https://example-bakery.test/baking-questions/</link><dc:creator>sam</dc:creator><wp:post_id>3</wp:post_id><wp:status>publish</wp:status><wp:post_type>topic</wp:post_type><content:encoded><![CDATA[<p>Ask about the rye.</p>]]></content:encoded></item>
<item><title>A note</title><link>https://example-bakery.test/baking-questions/note/</link><dc:creator>ann@example.com</dc:creator><wp:post_id>4</wp:post_id><wp:post_parent>3</wp:post_parent><wp:status>publish</wp:status><wp:post_type>reply</wp:post_type><content:encoded><![CDATA[<p>The crumb was open.</p>]]></content:encoded></item>`
	}
	assertClean := func(t *testing.T, r *Result) {
		t.Helper()
		for name, b := range r.Files {
			s := string(b)
			if strings.Contains(name, "example.com") || strings.Contains(name, "annexamplecom") || strings.Contains(s, "example.com") || strings.Contains(s, "annexamplecom") {
				t.Fatalf("author address leaked in %s", name)
			}
		}
	}
	t.Run("display name", func(t *testing.T) {
		r := convertWXR(t, bakeryChannel("en", "", "", "", items("Ann Lee")), nil, nil)
		assertClean(t, r)
		archive := string(r.Files["author/ann-lee/index.html"])
		post := string(r.Files["rye-morning/index.html"])
		topic := string(r.Files["baking-questions/index.html"])
		if archive == "" || !strings.Contains(archive, "Posts by Ann Lee") {
			t.Fatal("archive", archive)
		}
		if !strings.Contains(post, `class="byline"`) || !strings.Contains(post, "Ann Lee") || !strings.Contains(post, "author/ann-lee/") {
			t.Fatal("byline", post)
		}
		if !strings.Contains(topic, "<h2>Ann Lee</h2>") {
			t.Fatal("reply heading", topic)
		}
		if r.Files["author/annexamplecom/index.html"] != nil || r.Files["author/sam/index.html"] == nil {
			t.Fatal("slugs", r.Files)
		}
		if !strings.Contains(string(r.Files["author/sam/index.html"]), "Posts by Sam Cole") {
			t.Fatal("second author")
		}
	})
	t.Run("email display", func(t *testing.T) {
		r := convertWXR(t, bakeryChannel("en", "", "", "", items("ann@example.com")), nil, nil)
		assertClean(t, r)
		archive := string(r.Files["author/author/index.html"])
		post := string(r.Files["rye-morning/index.html"])
		topic := string(r.Files["baking-questions/index.html"])
		if archive == "" || !strings.Contains(archive, `<h1 class="entry-title">Posts</h1>`) || strings.Contains(archive, "Posts by") {
			t.Fatal("blank archive title", archive)
		}
		if strings.Contains(post, "byline") || strings.Contains(topic, "<h2>") {
			t.Fatal("email shown", post, topic)
		}
		if r.Files["author/annexamplecom/index.html"] != nil || r.Files["author/ann-lee/index.html"] != nil {
			t.Fatal("email slug kept")
		}
	})
}

func TestRealExportReviewFollowUps(t *testing.T) {
	wxr := func(base, content string) string {
		return `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:wp="http://wordpress.org/export/1.2/"><channel><title>Example Bakery</title><link>https://bakery.example</link>` + base + `<wp:wxr_version>1.2</wp:wxr_version>` +
			`<item><title>Story</title><link>https://bakery.example/story/</link><content:encoded><![CDATA[` + content + `]]></content:encoded><wp:post_id>1</wp:post_id><wp:post_name>story</wp:post_name><wp:status>publish</wp:status><wp:post_type>page</wp:post_type></item></channel></rss>`
	}
	run := func(x string) *Result {
		res, err := Convert(context.Background(), Input{Site: "s", WXR: []io.Reader{strings.NewReader(x)}})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	// Bracketed hyphenated words inside a sentence are prose, not plugins.
	res := run(wxr(`<wp:base_site_url>https://bakery.example</wp:base_site_url>`, "<p>We follow a [farm-to-table] idea and host a [multi-day] class.</p>\n\n[theme-my-login]\n\n<p>[powr-weather id=3] Done.</p>"))
	page := string(res.Files["story/index.html"])
	if !strings.Contains(page, "[farm-to-table]") || !strings.Contains(page, "[multi-day]") {
		t.Fatalf("prose in brackets was removed: %s", page)
	}
	if strings.Contains(page, "theme-my-login") || strings.Contains(page, "powr-weather") {
		t.Fatalf("plugin shortcodes printed: %s", page)
	}
	// A WordPress post that only mentions a Squarespace address stays WordPress.
	res = run(wxr("", `<p>We left our old host at static.squarespace.com and sqs-block markup last year.</p>`))
	if res.Report.Platform == "Squarespace" {
		t.Fatal("mention of Squarespace detected as a Squarespace export")
	}
	// Squarespace lazy images promote only http(s) or site paths into src.
	res = run(wxr("", `<div class="sqs-block image-block"><img data-src="javascript:alert(1)" data-image="data:text/html,x"></div>`))
	if res.Report.Platform != "Squarespace" {
		t.Fatal("Squarespace block not detected")
	}
	if p := string(res.Files["story/index.html"]); strings.Contains(p, ` src="javascript:`) || strings.Contains(p, ` src="data:`) {
		t.Fatalf("unsafe lazy image source promoted: %s", p)
	}
}
