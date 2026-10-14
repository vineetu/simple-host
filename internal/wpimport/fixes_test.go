package wpimport

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"
)

func TestRealExportFixes(t *testing.T) {
	for _, name := range []string{"bakery-wxr.xml", "vannelli-wxr.xml"} {
		t.Run(name, func(t *testing.T) {
			_, r := fixture(t, name)
			home := string(r.Files["index.html"])
			if r.Report.FrontPage != "page:/" || strings.Contains(home, `class="entry-title"`) || !strings.Contains(home, "<title>Home - ") {
				t.Error("static home title/path", r.Report.FrontPage)
			}
			for _, f := range r.Report.Features {
				for _, p := range f.Pages {
					if !strings.HasPrefix(p, "/") && p != footerPage {
						t.Errorf("feature %s has non-site path %q", f.Kind, p)
					}
				}
			}
			for p, b := range r.Files {
				if strings.HasSuffix(p, ".html") && strings.Contains(string(b), "<h2>WordPress form</h2>") {
					t.Error("generic visible form title", p)
				}
			}
			contact := featureByKind(r, FeatureContactForm)
			if contact == nil || len(contact.Pages) != 1 || contact.Pages[0] != "/contact/" {
				t.Errorf("contact pages: %+v", contact)
			}
			if name == "bakery-wxr.xml" {
				f := featureByKind(r, FeatureComments)
				if f == nil || f.Count != 3 || !f.Moderated || r.Report.Comments != 3 {
					t.Errorf("approved comments: %+v", f)
				}
				post := string(r.Files["2026/06/why-croissants-take-three-days/index.html"])
				for _, want := range []string{"2 June 2026", `<time datetime=`} {
					if !strings.Contains(post, want) {
						t.Error("post lost", want)
					}
				}
				if !strings.Contains(string(r.Files["about/index.html"]), "<figcaption>A morning at the bakery</figcaption>") {
					t.Error("embed caption lost")
				}
				if strings.Contains(post, `class="featured-image"`) {
					t.Error("featured image duplicated")
				}
				if strings.Contains(string(r.Files["search-index.json"]), "youtube.com/watch") {
					t.Error("bare URL in search text")
				}
			} else {
				// Comments were open on WordPress.com with none exported:
				// the reply form is there, with no count.
				if f := featureByKind(r, FeatureComments); f == nil || f.Count != 0 || len(f.Pages) == 0 {
					t.Errorf("open comments with none kept: %+v", f)
				}
				f := featureByKind(r, FeatureForum)
				if f == nil || len(f.Pages) != 1 || f.Pages[0] != "/vannelliworld/" {
					t.Errorf("forum paths: %+v", f)
				}
				forum := string(r.Files["vannelliworld/index.html"])
				for _, want := range []string{"Vannelli World Forum", "Sign in with your email to post", "No topics yet. Start the first one.", "<h2>New topic</h2>", `data-sh-new`, `data-sh-reply`, `data-sh-signin`, `>Post</button>`} {
					if !strings.Contains(forum, want) {
						t.Error("forum lost", want)
					}
				}
				if strings.Contains(forum, `data-sh-feature="contact"`) {
					t.Error("forum converted to contact")
				}
				subscribe := featureByKind(r, FeatureSubscribe)
				if subscribe == nil || subscribe.Count != 1 || len(subscribe.Pages) != 1 || subscribe.Pages[0] != footerPage {
					t.Errorf("footer subscribe: %+v", subscribe)
				}
				counter := featureByKind(r, FeatureHitCounter)
				if counter == nil || !strings.Contains(counter.Note, "/guestbook/") {
					t.Errorf("counter leftover note: %+v", counter)
				}
				for p, b := range r.Files {
					if !strings.HasSuffix(p, ".html") {
						continue
					}
					root := fragment(string(b))
					walk(root, func(n *xhtml.Node) {
						if n.Data == "footer" && strings.Contains(serialize(n), "Moved from WordPress") && !strings.Contains(serialize(n), `data-sh-feature="subscribe"`) {
							t.Error("missing footer widget", p)
						}
						if getAttr(n, "class") == "entry-content" && strings.Contains(serialize(n), `class="wp-block-navigation"`) {
							t.Error("duplicate content navigation", p)
						}
					})
				}
				if r.Report.MediaUnused != 2 {
					t.Error("unused attachments", r.Report.MediaUnused)
				}
				if r.Report.Dropped["external_asset"] != 0 {
					t.Error("content images dropped")
				}
			}
		})
	}
}

func TestUnknownBracketsAndPluginShortcodes(t *testing.T) {
	c := testConverter()
	nested := c.renderContent(`[editorial]Keep [audio src="song.mp3"][/editorial]`, "/", 0)
	if !strings.Contains(nested, `[editorial]`) || !strings.Contains(nested, `<audio controls`) || !strings.Contains(nested, `[/editorial]`) {
		t.Error("registered shortcode inside unknown wrapper", nested)
	}
	source := `Words [laughs] [sic] [from the archive] [unknown attr=yes] [unknown]inside[/unknown] [new_plugin] [new_plugin id=2] [new_plugin]keep[/new_plugin] [et_pb_section]Divi[/et_pb_section] [vc_row]Composer[/vc_row] [su_box]Ultimate[/su_box] [elementor-template id=2]`
	got := c.renderContent(source, "/interview/", 0)
	for _, want := range []string{"[laughs]", "[sic]", "[from the archive]", "[unknown attr=yes]", "[unknown]inside[/unknown]", "[new_plugin]", "keep", "Divi", "Composer", "Ultimate"} {
		if !strings.Contains(got, want) {
			t.Error("lost", want, got)
		}
	}
	for _, tag := range []string{"new_plugin", "et_pb_section", "vc_row", "su_box", "elementor-template"} {
		if c.result.Report.Dropped["shortcode:"+tag] == 0 {
			t.Error("plugin not counted", tag)
		}
	}
	if c.result.Report.Dropped["shortcode:new_plugin"] != 2 {
		t.Error("bare underscore text removed")
	}
}

func TestNavigationMatchingAndDistinctMenu(t *testing.T) {
	c := testConverter()
	c.headerMenu = []*menuItem{{URL: "/about/", Title: "About"}, {URL: "https://example.org/contact/", Title: "Contact"}}
	same := `<!-- wp:navigation --><!-- wp:navigation-link {"url":"/contact/"} /--><!-- wp:navigation-link {"url":"https://example.org/about/"} /--><!-- /wp:navigation -->`
	if out := c.renderContent(same, "/", 0); out != "" {
		t.Error("same URL set kept", out)
	}
	other := `<!-- wp:navigation --><!-- wp:navigation-link {"url":"https://outside.test/","label":"Elsewhere"} /--><!-- /wp:navigation -->`
	got := c.renderContent(other, "/", 0)
	if !strings.Contains(got, `class="wp-block-navigation"`) || !strings.Contains(got, "<summary>Menu</summary>") {
		t.Error("distinct nav missing responsive markup", got)
	}
}

func TestExternalImagesKeptAndProxyFetched(t *testing.T) {
	c := testConverter()
	proxy := "https://i0.wp.com/images.other.test/photos/a.png?w=200"
	c.data.Items = []*item{{Type: "page", Status: "publish", Content: `<img src="` + proxy + `"><img src="https://other.test/b.jpg"><img src="https://other.test/b.jpg">`}}
	var fetched []string
	image := testPNG(t)
	c.in.Fetch = func(_ context.Context, u string) ([]byte, error) { fetched = append(fetched, u); return image, nil }
	c.copyMedia()
	if len(fetched) != 1 || fetched[0] != proxy || c.result.Files["wp-content/uploads/external/images.other.test/photos/a.png"] == nil {
		t.Error("proxy not copied", fetched, sortedKeys(c.result.Files))
	}
	got := c.cleanContent(c.data.Items[0].Content, "/page/")
	if !strings.Contains(got, `../wp-content/uploads/external/images.other.test/photos/a.png`) || strings.Count(got, `https://other.test/b.jpg`) != 2 || len(c.externalImages) != 1 {
		t.Error("external content lost or warning duplicated", got, c.result.Report.Warnings)
	}
	if c.result.Report.Dropped["external_asset"] != 0 {
		t.Error("external img dropped")
	}
}

func TestUsedMediaOriginalsAndArchivePaths(t *testing.T) {
	for _, prefix := range []string{"uploads/", "wp-content/uploads/", "backup/", "site/", ""} {
		t.Run(prefix, func(t *testing.T) {
			c := testConverter()
			data := testPNG(t)
			m := MediaFile{Size: int64(len(data)), Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }}
			c.in.Media = map[string]MediaFile{}
			for _, name := range []string{"photo.png", "photo-scaled.png", "photo-300x200.png", "unused.png", "gallery.png", "featured.png", "menu.png", "reusable.png", "document.pdf"} {
				c.in.Media[prefix+"2026/07/"+name] = m
			}
			attachment := func(id, name string) *item {
				return &item{ID: id, Type: "attachment", Attachment: "https://example.org/wp-content/uploads/2026/07/" + name}
			}
			for _, i := range []*item{attachment("1", "photo-scaled.png"), attachment("2", "unused.png"), attachment("3", "gallery.png"), attachment("4", "featured.png"), attachment("5", "menu.png"), attachment("6", "reusable.png")} {
				c.data.Items = append(c.data.Items, i)
				c.byID[i.ID] = i
			}
			c.byID["7"] = &item{Type: "wp_block", Content: `<img src="https://example.org/wp-content/uploads/2026/07/reusable.png">`}
			c.headerMenu = []*menuItem{{URL: c.byID["5"].Attachment}}
			source := `<img src="https://example.org/wp-content/uploads/2026/07/photo-300x200.png"><a href="https://example.org/wp-content/uploads/2026/07/document.pdf">Download</a><!-- wp:gallery {"ids":[3]} /--><!-- wp:block {"ref":7} /-->`
			c.data.Items = append(c.data.Items, &item{Type: "page", Status: "publish", Content: source, Meta: []meta{{Key: "_thumbnail_id", Value: "4"}}})
			c.copyMedia()
			if c.result.Report.MediaCopied != 6 || c.result.Report.MediaUnused != 1 {
				t.Errorf("media counts: %+v", c.result.Report)
			}
			if c.result.Files["wp-content/uploads/2026/07/photo-scaled.png"] == nil || c.result.Files["wp-content/uploads/2026/07/document.pdf"] == nil {
				t.Error("scaled/linked media lost")
			}
			for _, name := range []string{"photo.png", "photo-300x200.png", "unused.png"} {
				if c.result.Files["wp-content/uploads/2026/07/"+name] != nil {
					t.Error("copied unwanted file", name)
				}
			}
			if got := c.rewriteURL("https://example.org/wp-content/uploads/2026/07/photo-300x200.png", "/post/"); got != "../wp-content/uploads/2026/07/photo-scaled.png" {
				t.Error("size variant not canonical", got)
			}
		})
	}
}

func TestDatesExcerptsAndCounterWords(t *testing.T) {
	if got := dateHTML("2026-10-14 22:01:00"); !strings.Contains(got, ">14 October 2026</time>") || !strings.Contains(got, `datetime="2026-10-14T22:01:00Z"`) {
		t.Error(got)
	}
	source := `<ol><li>Day one: the dough</li><li>Day two: bake</li></ol><p>https://youtu.be/abcdef</p>[gallery ids=1]`
	if got := cleanText(source); strings.TrimSpace(got) != "Day one: the dough Day two: bake" {
		t.Error("excerpt flattening", got)
	}
	c := testConverter()
	got := c.renderContent("YOU'RE VISITOR NUMBER [mwr_hit_counter]: THANKS!", "/counter/", 0)
	if !strings.Contains(got, "THANKS!") || !strings.Contains(c.features[FeatureHitCounter].Note, "/counter/") {
		t.Error("inline counter note", got, c.features[FeatureHitCounter])
	}
}

func TestContactTitlesAndWidgetForms(t *testing.T) {
	c := testConverter()
	forum := c.cleanContent(c.widget(FeatureForum, "Asgaros", "/forum/", nil), "/forum/")
	if c.features[FeatureContactForm] != nil || !strings.Contains(forum, `data-sh-new`) || !strings.Contains(forum, `data-sh-reply`) {
		t.Error("importer forms redetected", forum)
	}
	titled := c.cleanContent(`<form aria-label="Ask the baker"><input name="message"></form>`, "/contact/")
	if !strings.Contains(titled, "<h2>Ask the baker</h2>") || strings.Contains(titled, "<h2>WordPress form</h2>") {
		t.Error("form title lost", titled)
	}
}

func TestExternalImageWarningCount(t *testing.T) {
	wxr := `<rss xmlns:wp="http://wordpress.org/export/1.2/" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><title>Site</title><link>https://example.org</link><item><title>Home</title><link>https://example.org/</link><wp:post_id>1</wp:post_id><wp:post_type>page</wp:post_type><wp:status>publish</wp:status><content:encoded><![CDATA[<img src="https://other.test/a.jpg"><img src="https://other.test/a.jpg">]]></content:encoded></item></channel></rss>`
	r, err := Convert(context.Background(), Input{WXR: []io.Reader{strings.NewReader(wxr)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Warnings) != 1 || !strings.Contains(r.Report.Warnings[0], "Kept 1 external content image URLs") || strings.Count(r.Report.Warnings[0], "https://other.test/a.jpg") != 1 {
		t.Error("external image warning counts", r.Report.Warnings)
	}
	if strings.Count(string(r.Files["index.html"]), `src="https://other.test/a.jpg"`) != 2 {
		t.Error("external images not retained")
	}
}

func TestFooterMediaOnNestedPages(t *testing.T) {
	c := testConverter()
	c.in.Media = testMedia()
	c.data.Items = []*item{{Type: "wp_template_part", Slug: "footer", Content: `<p>Footer</p><img src="https://example.org/wp-content/uploads/2026/07/photo.png"><style>.footer{background:url("https://example.org/wp-content/uploads/2026/07/photo.png")}</style>`}}
	c.copyMedia()
	c.templateParts()
	c.putPage("/deep/page/", "Page", "", "", nil)
	got := string(c.result.Files["deep/page/index.html"])
	if !strings.Contains(got, `src="../../wp-content/uploads/2026/07/photo.png"`) || !strings.Contains(got, `url("../../wp-content/uploads/2026/07/photo.png")`) {
		t.Error("footer media not rebased", got)
	}
}
