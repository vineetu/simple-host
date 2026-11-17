package wpimport

import (
	"context"
	"strings"
	"testing"
)

func testConverter() *converter {
	return &converter{ctx: context.Background(), in: Input{Site: "test-site"}, limits: DefaultLimits, data: &export{Title: "Site", Description: "Tagline"}, result: &Result{Files: map[string][]byte{}, Report: Report{Dropped: map[string]int{}}}, byID: map[string]*item{}, hosts: map[string]bool{"example.org": true, "www.example.org": true}, terms: map[string]term{}, media: map[string]string{}, mediaOriginal: map[string]string{}, features: map[string]*Feature{}, postsPath: "/"}
}
func TestBlockParser(t *testing.T) {
	for _, tt := range []struct {
		name, input string
		names       []string
		children    int
		attr        string
	}{{"static", `<!-- wp:paragraph --><p>Hello</p><!-- /wp:paragraph -->`, []string{"core/paragraph"}, 1, ""}, {"nesting", `<!-- wp:group {"className":"box"} --><div><!-- wp:paragraph --><p>Hello</p><!-- /wp:paragraph --></div><!-- /wp:group -->`, []string{"core/group"}, 3, "box"}, {"self closing", `<!-- wp:latest-posts /--><!-- wp:search {"label":"Find"} /-->`, []string{"core/latest-posts", "core/search"}, 0, ""}, {"unknown html", `before<!-- wp:plugin/card --><section>Card</section><!-- /wp:plugin/card -->after`, []string{"", "plugin/card", ""}, 0, ""}, {"malformed attrs", `<!-- wp:group {broken} --><p>Keep this</p><!-- /wp:group -->`, []string{"core/group"}, 1, ""}, {"unclosed", `<!-- wp:group --><p>Keep</p>`, []string{"core/group"}, 1, ""}} {
		t.Run(tt.name, func(t *testing.T) {
			bs := parseBlocks(tt.input)
			if len(bs) != len(tt.names) {
				t.Fatalf("%+v", bs)
			}
			for n, name := range tt.names {
				if bs[n].Name != name {
					t.Error(bs[n].Name, name)
				}
			}
			if len(bs[0].Children) != tt.children {
				t.Errorf("children: %d", len(bs[0].Children))
			}
			if tt.attr != "" && attrString(bs[0].Attrs, "className") != tt.attr {
				t.Error("attribute lost")
			}
			c := testConverter()
			out := c.renderBlocks(bs, "/", 0)
			if strings.Contains(out, "<!-- wp:") {
				t.Error("comments retained")
			}
		})
	}
}
func TestWPAutop(t *testing.T) {
	for _, tt := range []struct {
		name, input string
		want        []string
		absent      []string
	}{{"paragraphs", "One line\nNext line\n\nA second paragraph", []string{"<p>One line<br>\nNext line</p>", "<p>A second paragraph</p>"}, nil}, {"saved HTML", "<h2>Title</h2>\n<p>Already rendered</p>\n<div><p>Inside</p></div>", []string{"<h2>", "Already rendered", "Inside"}, []string{"<p><h2>", "<p><p>", "<p><div>"}}, {"pre", "Before\n\n<pre>one\ntwo\n\nthree</pre>\n\nAfter", []string{"<pre>one\ntwo\n\nthree</pre>", "<p>After</p>"}, []string{"<pre>one<br>"}}, {"script", "<script>\na()\nb()\n</script>", []string{"<script>\na()\nb()\n</script>"}, []string{"<br>"}}, {"list", "<ul>\n<li>one</li>\n<li>two</li>\n</ul>", []string{"<li>", "one"}, []string{"<p><li>", "<p><ul>"}}, {"empty", "", nil, []string{"<p>"}}} {
		t.Run(tt.name, func(t *testing.T) {
			got := wpautop(tt.input)
			for _, s := range tt.want {
				if !strings.Contains(got, s) {
					t.Errorf("%q missing from %q", s, got)
				}
			}
			for _, s := range tt.absent {
				if strings.Contains(got, s) {
					t.Errorf("%q present in %q", s, got)
				}
			}
		})
	}
}
func TestShortcodes(t *testing.T) {
	for _, tt := range []struct{ name, input, contains, dropped string }{{"quoted", `[contact-form-7 id='40' title="Hi there"]`, `data-sh-feature="contact"`, ""}, {"unquoted", `[wpforms id=5 /]`, `data-sh-feature="contact"`, ""}, {"escaped", `[[contact-form-7 id=40]]`, `[contact-form-7 id=40]`, ""}, {"enclosed", `[unknown]Keep [other]inside[/other][/unknown]`, `[unknown]Keep [other]inside[/other][/unknown]`, ``}, {"bracket prose", `This is [Not a shortcode] and [1, 2].`, `[Not a shortcode]`, ""}, {"youtube", `[youtube https://youtu.be/abcdefghi12]`, `youtube-nocookie.com/embed/abcdefghi12`, ""}, {"bare embed", "https://vimeo.com/123456", `player.vimeo.com/video/123456`, ""}, {"caption", `[caption]<img src="x.png">Beach scene[/caption]`, `<figcaption>Beach scene</figcaption>`, ""}, {"unknown self-closing", `Before [plugin arg=yes /] after`, `[plugin arg=yes /]`, ``}} {
		t.Run(tt.name, func(t *testing.T) {
			c := testConverter()
			got := c.renderContent(tt.input, "/", 0)
			if !strings.Contains(got, tt.contains) {
				t.Fatalf("%q does not contain %q", got, tt.contains)
			}
			if tt.dropped != "" && c.result.Report.Dropped[tt.dropped] != 1 {
				t.Errorf("drops %v", c.result.Report.Dropped)
			}
		})
	}
	scs := []shortcode{}
	renderShortcodes(`[x a="hello world" b='quoted' c=plain /]`, func(s shortcode) string { scs = append(scs, s); return "" })
	if len(scs) != 1 || attrString(scs[0].Attrs, "a") != "hello world" || attrString(scs[0].Attrs, "c") != "plain" {
		t.Fatal(scs)
	}
}
func TestDynamicBlocks(t *testing.T) {
	for _, tt := range []struct{ name, content, want, drop string }{{"embed", `<!-- wp:embed {"url":"https://youtu.be/abcdefghijk"} /-->`, `youtube-nocookie`, ""}, {"query", `<!-- wp:query --><!-- wp:post-template /--><!-- /wp:query -->`, `Latest post`, ""}, {"site title", `<!-- wp:site-title /-->`, `Site`, ""}, {"unknown saved", `<!-- wp:plugin/card --><h3>Saved card</h3><!-- /wp:plugin/card -->`, `Saved card`, ""}, {"unknown empty", `<!-- wp:plugin/card /-->`, "", "block:plugin/card"}, {"calendar", `<!-- wp:calendar /-->`, "", "block:core/calendar"}, {"jetpack form", `<!-- wp:jetpack/contact-form --><!-- wp:jetpack/field-email {"label":"Email address","required":true} /--><!-- /wp:jetpack/contact-form -->`, `type="email"`, ""}, {"slideshow", `<!-- wp:jetpack/slideshow --><div class="wp-block-jetpack-slideshow"><img src="x.png"></div><!-- /wp:jetpack/slideshow -->`, `wp-block-jetpack-slideshow`, ""}} {
		t.Run(tt.name, func(t *testing.T) {
			c := testConverter()
			c.posts = []*item{{Title: "Latest post", Path: "/latest/"}}
			out := c.renderContent(tt.content, "/", 0)
			if !strings.Contains(out, tt.want) {
				t.Error(out)
			}
			if tt.drop != "" && c.result.Report.Dropped[tt.drop] != 1 {
				t.Error(c.result.Report.Dropped)
			}
		})
	}
	c := testConverter()
	c.byID["9"] = &item{Content: `<!-- wp:block {"ref":9} /-->`}
	if s := c.renderContent(`<!-- wp:block {"ref":9} /-->`, "/", 0); s != "" || c.result.Report.Dropped["block:core/block"] != 1 {
		t.Error("recursive reusable block not bounded")
	}
}
