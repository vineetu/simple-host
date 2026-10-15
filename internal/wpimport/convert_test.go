package wpimport

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"
)

func testMedia() map[string]MediaFile {
	out := map[string]MediaFile{}
	for _, name := range []string{"photo.png", "second.jpg"} {
		im := image.NewNRGBA(image.Rect(0, 0, 64, 48))
		for y := 0; y < 48; y++ {
			for x := 0; x < 64; x++ {
				im.Set(x, y, color.NRGBA{uint8(x * 4), uint8(y * 5), 100, 255})
			}
		}
		var b bytes.Buffer
		if strings.HasSuffix(name, "png") {
			_ = png.Encode(&b, im)
		} else {
			_ = jpeg.Encode(&b, im, nil)
		}
		data := b.Bytes()
		out["2026/07/"+name] = MediaFile{Size: int64(len(data)), Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }}
	}
	return out
}
func fixture(t *testing.T, name string) ([]byte, *Result) {
	t.Helper()
	b, e := os.ReadFile("testdata/" + name)
	if e != nil {
		t.Fatal(e)
	}
	r, e := Convert(context.Background(), Input{Site: "moved", WXR: []io.Reader{bytes.NewReader(b)}, Media: testMedia()})
	if e != nil {
		t.Fatal(e)
	}
	return b, r
}
func featureByKind(r *Result, kind string) *Feature {
	for _, f := range r.Report.Features {
		if f.Kind == kind {
			return &f
		}
	}
	return nil
}
func checkInvariants(t *testing.T, b []byte, r *Result) {
	t.Helper()
	d, _, e := parseWXR(context.Background(), []io.Reader{bytes.NewReader(b)}, DefaultLimits)
	if e != nil {
		t.Fatal(e)
	}
	planner := &converter{byID: map[string]*item{}, result: &Result{}}
	for _, i := range d.Items {
		planner.byID[i.ID] = i
	}
	for _, i := range d.Items {
		if i.public() && isPageType(i.Type) {
			p := planner.permalink(i, map[string]bool{})
			if _, ok := r.Files[filePath(p)]; !ok {
				t.Errorf("published %s missing at %s", i.Title, p)
			}
		}
	}
	var total int64
	for name, b := range r.Files {
		total += int64(len(b))
		if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") {
			t.Errorf("unsafe output path %q", name)
		}
		if !strings.HasSuffix(name, ".html") {
			continue
		}
		if strings.Contains(string(b), "<!-- wp:") || strings.Contains(string(b), "[contact-form") {
			t.Errorf("editor syntax remains in %s", name)
		}
		doc, e := xhtml.Parse(bytes.NewReader(b))
		if e != nil {
			t.Fatal(e)
		}
		base, _ := url.Parse("https://moved.test/" + name)
		walk(doc, func(n *xhtml.Node) {
			for _, a := range n.Attr {
				if a.Key != "href" && !(n.Data == "img" && a.Key == "src") {
					continue
				}
				u, e := url.Parse(a.Val)
				if e != nil {
					t.Errorf("bad URL %s", a.Val)
					continue
				}
				if u.IsAbs() || u.Host != "" || a.Val == "" || strings.HasPrefix(a.Val, "#") {
					continue
				}
				u = base.ResolveReference(u)
				p := strings.TrimPrefix(u.Path, "/")
				if p == "" || strings.HasSuffix(p, "/") {
					p += "index.html"
				}
				if _, ok := r.Files[p]; !ok {
					t.Errorf("%s references missing %s (%s)", name, p, a.Val)
				}
			}
		})
	}
	if total != r.Report.TotalBytes || total > DefaultLimits.MaxSiteBytes {
		t.Errorf("size accounting %d != %d", total, r.Report.TotalBytes)
	}
	for _, p := range []string{"index.html", "search/index.html", "search-index.json", "404.html", "sitemap.xml", "robots.txt", "sh-import/theme.css"} {
		if len(r.Files[p]) == 0 {
			t.Errorf("missing %s", p)
		}
	}
}
func TestExportFixtures(t *testing.T) {
	names, e := filepath.Glob("testdata/*")
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range names {
		if filepath.Ext(name) != ".xml" && filepath.Ext(name) != ".zip" {
			continue
		}
		t.Run(filepath.Base(name), func(t *testing.T) {
			var docs [][]byte
			if filepath.Ext(name) == ".zip" {
				z, e := zip.OpenReader(name)
				if e != nil {
					t.Fatal(e)
				}
				defer z.Close()
				for _, f := range z.File {
					if strings.EqualFold(filepath.Ext(f.Name), ".xml") {
						r, e := f.Open()
						if e != nil {
							t.Fatal(e)
						}
						b, e := io.ReadAll(r)
						_ = r.Close()
						if e != nil {
							t.Fatal(e)
						}
						docs = append(docs, b)
					}
				}
			} else {
				b, e := os.ReadFile(name)
				if e != nil {
					t.Fatal(e)
				}
				docs = append(docs, b)
			}
			if len(docs) == 0 {
				t.Fatal("fixture has no XML")
			}
			readers := func() []io.Reader {
				var rs []io.Reader
				for _, b := range docs {
					rs = append(rs, bytes.NewReader(b))
				}
				return rs
			}
			in := Input{Site: "moved", WXR: readers(), Media: testMedia()}
			r, e := Convert(context.Background(), in)
			if e != nil {
				t.Fatal(e)
			}
			for _, b := range docs {
				checkInvariants(t, b, r)
			}
			in.WXR = readers()
			again, e := Convert(context.Background(), in)
			if e != nil || !reflect.DeepEqual(r, again) {
				t.Fatal("import is not deterministic", e)
			}
			if strings.HasSuffix(name, "wordpress-org.xml") {
				if r.Report.FrontPage != "page:/home/" || r.Report.MenuItems != 3 || r.Report.Comments != 1 {
					t.Fatalf("org report: %+v", r.Report)
				}
				f := featureByKind(r, FeatureComments)
				if f == nil || !f.Moderated || f.Count != 1 {
					t.Fatalf("comments: %+v", f)
				}
				for _, p := range []string{"category/travel/italy/index.html", "tag/family/index.html", "blog/index.html"} {
					if r.Files[p] == nil {
						t.Error("missing", p)
					}
				}
				h := string(r.Files["2026/07/italy/index.html"])
				for _, s := range []string{"youtube-nocookie.com/embed/dQw4w9WgXcQ", "../../../wp-content/uploads/2026/07/photo.png", "Lovely! &lt;script&gt;", "data-moderated=\"true\""} {
					if !strings.Contains(h, s) {
						t.Errorf("org content missing %s", s)
					}
				}
				contact := string(r.Files["contact/index.html"])
				for _, s := range []string{`name="your-name" required`, `type="email"`, `<select`, `Travel`, `name="consent"`} {
					if !strings.Contains(contact, s) {
						t.Error("missing form field", s)
					}
				}
				if !strings.Contains(string(r.Files["index.html"]), "Come back soon.") {
					t.Error("reusable block lost")
				}
			}
			if strings.HasSuffix(name, "wordpress-com.xml") {
				for _, kind := range []string{FeatureContactForm, FeatureGuestbook, FeatureForum, FeatureHitCounter, FeatureSubscribe, FeatureShop, FeatureSearch} {
					if featureByKind(r, kind) == nil {
						t.Error("missing feature", kind)
					}
				}
				if r.Report.FrontPage != "posts" {
					t.Error(r.Report.FrontPage)
				}
				h := string(r.Files["pictures/index.html"])
				for _, s := range []string{"Our beach.", "Keep these words.", "youtube-nocookie.com/embed/", "player.vimeo.com/video/123456"} {
					if !strings.Contains(h, s) {
						t.Error("com content missing", s)
					}
				}
				if r.Report.Dropped["shortcode:unknown-plugin"] != 0 || !strings.Contains(h, "[unknown-plugin") {
					t.Error("unregistered shortcode was removed")
				}
				if !strings.Contains(string(r.Files["forum/index.html"]), "Earlier topics") {
					t.Error("bbPress archive absent")
				}
				if string(r.Files["sh-import/payment-links.json"]) != "{}" {
					t.Error("payment map absent")
				}
			}
		})
	}
}
func minimalXML(content string) string {
	return `<rss xmlns:wp="http://wordpress.org/export/1.2/" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><title>Test</title><link>https://example.org</link><item><title>Post</title><link>https://example.org/post/</link><wp:post_id>1</wp:post_id><wp:post_name>post</wp:post_name><wp:post_type>post</wp:post_type><wp:status>publish</wp:status><content:encoded><![CDATA[` + content + `]]></content:encoded></item></channel></rss>`
}
func TestXMLSafety(t *testing.T) {
	for _, tt := range []struct {
		name, doc string
		limits    Limits
		wantErr   bool
		warnings  int
	}{
		{name: "entities", doc: `<!DOCTYPE rss [<!ENTITY lol "lol"><!ENTITY x "&lol;&lol;&lol;">]>` + minimalXML("&x;"), wantErr: true},
		{name: "external", doc: `<!DOCTYPE rss [<!ENTITY x SYSTEM "file:///etc/passwd">]>` + minimalXML("&x;"), wantErr: true},
		{name: "any doctype", doc: `<!DOCTYPE rss SYSTEM "https://invalid.example/wxr.dtd">` + minimalXML("safe"), wantErr: true},
		{name: "bytes", doc: minimalXML("large"), limits: Limits{MaxXMLBytes: 10}, wantErr: true},
		{name: "items", doc: strings.Replace(minimalXML("a"), "</channel>", `<item/><item/></channel>`, 1), limits: Limits{MaxItems: 1}, wantErr: true},
		{name: "ampersand", doc: strings.Replace(minimalXML("safe"), "<title>Test</title>", "<title>Tom & Jerry</title>", 1), warnings: 1},
		{name: "invalid utf8", doc: minimalXML("a\xffb")},
		{name: "malformed", doc: `<rss><channel><item></channel></rss>`, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, w, e := parseWXR(context.Background(), []io.Reader{strings.NewReader(tt.doc)}, limitsWithDefaults(tt.limits))
			if (e != nil) != tt.wantErr {
				t.Fatalf("error %v", e)
			}
			if !tt.wantErr && len(w) != tt.warnings {
				t.Errorf("warnings %v", w)
			}
		})
	}
	_, _, e := parseWXR(context.Background(), []io.Reader{strings.NewReader(minimalXML("a")), strings.NewReader(minimalXML("b"))}, Limits{MaxXMLBytes: int64(len(minimalXML("a")) + 10), MaxItems: 10})
	if e == nil {
		t.Error("combined XML byte limit ignored")
	}
}
func TestFrontPageAndPlainPermalinks(t *testing.T) {
	for _, tt := range []struct{ classes, want, blog string }{{"page-id-2 home", "page:/landing/", "/blog/"}, {"home blog", "posts", "/"}, {"", "page:/home/", "/blog/"}} {
		t.Run(tt.classes, func(t *testing.T) {
			items := []*item{{ID: "1", Type: "page", Status: "publish", Slug: "home", Path: "/home/"}, {ID: "2", Type: "page", Status: "publish", Path: "/landing/"}}
			c := testConverter()
			c.published = items
			c.byID["2"] = items[1]
			c.bodyClasses = tt.classes
			_, blog := c.frontPage()
			if c.result.Report.FrontPage != tt.want || blog != tt.blog {
				t.Error(c.result.Report.FrontPage, blog)
			}
		})
	}
	c := testConverter()
	i := &item{ID: "123", Title: "Plain", Link: "https://example.org/?p=123"}
	if got := c.permalink(i, map[string]bool{}); got != "/p/123/" || len(c.result.Report.Warnings) == 0 {
		t.Fatal(got)
	}
	c.byID["1"] = &item{ID: "1", Slug: "parent", Type: "page"}
	if c.permalink(&item{ID: "2", Slug: "child", Type: "page", Parent: "1"}, map[string]bool{}) != "/parent/child/" {
		t.Error("hierarchical fallback")
	}
}
func TestMergedExportAndPagination(t *testing.T) {
	b, r := fixture(t, "wordpress-org.xml")
	merged, e := Convert(context.Background(), Input{WXR: []io.Reader{bytes.NewReader(b), bytes.NewReader(b)}, Media: testMedia()})
	if e != nil || merged.Report.Pages != r.Report.Pages {
		t.Fatal("split export deduplication", e)
	}
	c := testConverter()
	for n := 0; n < 42; n++ {
		c.posts = append(c.posts, &item{Title: "Post", Path: "/post/"})
	}
	c.archive("/blog/", "Blog", c.posts, nil)
	if c.result.Files["blog/page/3/index.html"] == nil {
		t.Error("pagination missing")
	}
	if strings.Count(string(c.result.Files["blog/index.html"]), "<h2>") != 20 {
		t.Error("archive does not have 20 entries")
	}
	var index []map[string]any
	c.published = []*item{{Path: "/post/", Title: "Post", Content: strings.Repeat("text ", 2000)}}
	c.search(nil)
	if json.Unmarshal(c.result.Files["search-index.json"], &index) != nil || len(index[0]["text"].(string)) > 5005 {
		t.Fatal("search text cap")
	}
}

func TestWXRVersionsAndDirectives(t *testing.T) {
	for _, version := range []string{"1.0", "1.1", "1.2"} {
		t.Run(version, func(t *testing.T) {
			doc := strings.Replace(minimalXML("Text"), `<item>`, `<item><excerpt:encoded xmlns:excerpt="http://wordpress.org/export/`+version+`/excerpt/">A useful excerpt</excerpt:encoded>`, 1)
			doc = strings.ReplaceAll(doc, "http://wordpress.org/export/1.2/", "http://wordpress.org/export/"+version+"/")
			e, _, err := parseWXR(context.Background(), []io.Reader{strings.NewReader(doc)}, DefaultLimits)
			if err != nil || e.Items[0].Excerpt != "A useful excerpt" {
				t.Fatal(e, err)
			}
		})
	}
	var declarations strings.Builder
	declarations.WriteString(`<!DOCTYPE rss [<!ENTITY lol "lol">`)
	for n := 1; n <= 9; n++ {
		previous := "lol"
		if n > 1 {
			previous = "lol" + jsonNumber(float64(n-1))
		}
		declarations.WriteString(`<!ENTITY lol` + jsonNumber(float64(n)) + ` "` + strings.Repeat("&"+previous+";", 10) + `">`)
	}
	declarations.WriteString("]>")
	doc := declarations.String() + minimalXML("&lol9;")
	if _, _, err := parseWXR(context.Background(), []io.Reader{strings.NewReader(doc)}, DefaultLimits); err == nil {
		t.Error("billion-laughs document accepted")
	}
	doc = strings.Replace(minimalXML("safe"), "<item>", `<item><!DOCTYPE nested [<!ENTITY hidden "unsafe">]>`, 1)
	if _, _, err := parseWXR(context.Background(), []io.Reader{strings.NewReader(doc)}, DefaultLimits); err == nil {
		t.Error("nested entity declaration was skipped")
	}
}

func TestUnreadableSplitPart(t *testing.T) {
	r, err := Convert(context.Background(), Input{WXR: []io.Reader{strings.NewReader("<rss><channel><item></channel></rss>"), strings.NewReader(minimalXML("A usable post"))}})
	if err != nil || r.Files["post/index.html"] == nil || len(r.Report.Warnings) == 0 {
		t.Fatal(r, err)
	}
}
