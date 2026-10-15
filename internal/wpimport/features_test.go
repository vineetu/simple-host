package wpimport

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestFeatureDetectionAndWidgets(t *testing.T) {
	for _, tt := range []struct{ kind, input, attr, preset, resource string }{{FeatureContactForm, `[contact-form-7 id=40]`, `data-sh-feature="contact"`, "inbox", "messages"}, {FeatureSubscribe, `<!-- wp:jetpack/subscriptions /-->`, `data-sh-feature="subscribe"`, "inbox", "subscribers"}, {FeatureGuestbook, `[gwolle_gb]`, `data-sh-feature="guestbook"`, "comments", "guestbook"}, {FeatureForum, `[bbp-forum-index]`, `data-sh-feature="forum"`, "wall", "forum"}, {FeatureHitCounter, `[jp_blog_stats]`, "", "", ""}, {FeatureShop, `<!-- wp:woocommerce/cart /-->`, "", "", ""}, {FeatureSubscribe, `[mc4wp_form]`, `data-sh-feature="subscribe"`, "inbox", "subscribers"}, {FeatureContactForm, `[gravityform id=1]`, `data-sh-feature="contact"`, "inbox", "messages"}, {FeatureGuestbook, `<!-- wp:gwolle-gb/guestbook /-->`, `data-sh-feature="guestbook"`, "comments", "guestbook"}} {
		t.Run(tt.kind+tt.input, func(t *testing.T) {
			c := testConverter()
			markup := c.renderContent(tt.input, "/test/", 0)
			f := c.features[tt.kind]
			if f == nil {
				t.Fatal("feature not detected")
			}
			if !strings.Contains(markup, tt.attr) {
				t.Error(markup)
			}
			c.configureFeature(f)
			if len(f.Pages) != 1 || f.Pages[0] != "/test/" {
				t.Error(f.Pages)
			}
			if tt.preset != "" {
				if len(f.Setup) != 2 {
					t.Fatal(f.Setup)
				}
				call := f.Setup[0]
				body := call.Args["body"].(map[string]any)
				if call.Tool != "storage_set_resource" || call.Args["site"] != "test-site" || call.Args["name"] != tt.resource || body["preset"] != tt.preset {
					t.Error(call)
				}
				if f.Setup[1].Tool != "storage_sql_schema" || !strings.HasPrefix(f.Setup[1].Args["sql"].(string), "CREATE TABLE IF NOT EXISTS ") {
					t.Error(f.Setup)
				}
				if tt.kind == FeatureGuestbook && body["approve_first"] != true {
					t.Error("guestbook needs approval")
				}
				if strings.Contains(markup, "onclick") || !strings.Contains(markup, `role="status"`) {
					t.Error("widget status missing")
				}
				if f.Status != StatusNeedsSetup {
					t.Error(f.Status)
				}
			} else if tt.kind == FeatureShop && f.Status != StatusNotSupported || tt.kind == FeatureHitCounter && f.Status != StatusReplaced {
				t.Error(f.Status)
			}
		})
	}
	c := testConverter()
	markup := c.comments(&item{Path: "/post/", CommentStatus: "open", Comments: []comment{{Approved: "1", Author: "<Ada>", Content: "<b>Hi</b>"}, {Approved: "0", Content: "held"}, {Approved: "spam", Content: "spam"}}})
	f := c.features[FeatureComments]
	c.configureFeature(f)
	if f.Count != 1 || !f.Moderated || !strings.Contains(markup, `data-sh-feature="comments"`) || strings.Contains(markup, "<b>Hi</b>") {
		t.Fatal(f, markup)
	}
	if !strings.Contains(markup, "&lt;Ada&gt;") || strings.Contains(markup, ">held<") {
		t.Error(markup)
	}
	if f.Setup[0].Args["body"].(map[string]any)["approve_first"] != true {
		t.Error(f.Setup)
	}
	c = testConverter()
	f = c.feature(FeatureComments, "WordPress", "/a/", 0)
	c.configureFeature(f)
	b, _ := json.Marshal(f.Setup[0])
	if strings.Contains(string(b), "approve_first") {
		t.Error("false approve_first must be omitted")
	}
	c.feature(FeatureComments, "WordPress", "/a/", 0)
	c.feature(FeatureComments, "WordPress", "/b/", 0)
	if len(f.Pages) != 2 {
		t.Error("feature pages not deduplicated")
	}
	for _, kind := range []string{FeatureSearch, FeatureHitCounter, FeatureShop} {
		c := testConverter()
		f := c.feature(kind, "WordPress", "/", 1)
		c.configureFeature(f)
		if len(f.Setup) != 0 {
			t.Errorf("%s should have no setup", kind)
		}
	}
}
func TestFormFields(t *testing.T) {
	for _, tt := range []struct {
		name, template string
		want           []string
	}{{"cf7", `<label>Full name [text* your-name]</label><label>Email [email* your-email]</label><label>Phone [tel phone]</label><label>URL [url url]</label><label>Number [number qty]</label><label>Date [date date]</label><label>Message [textarea message]</label><label>Choice [select choice "One" "Two"]</label><label>Options [checkbox opts "A" "B"]</label><label>Radio [radio r "X" "Y"]</label>[acceptance agree]Accept[/acceptance][submit "Go"]`, []string{`name="your-name" required`, `type="email"`, `type="tel"`, `type="url"`, `type="number"`, `type="date"`, `<textarea`, `<select`, `type="checkbox"`, `type="radio"`, `Full name`}}} {
		t.Run(tt.name, func(t *testing.T) {
			h := fieldsHTML(cf7Fields(tt.template))
			for _, s := range tt.want {
				if !strings.Contains(h, s) {
					t.Error("missing", s, h)
				}
			}
		})
	}
	fs := wpformsFields(`{"fields":{"1":{"id":"1","type":"email","label":"Your email","required":"1"},"2":{"id":"2","type":"paragraph-text","label":"Message"},"3":{"id":"3","type":"select","label":"Choose","choices":{"1":{"label":"Yes"},"2":{"label":"No"}}}}}`)
	h := fieldsHTML(fs)
	for _, s := range []string{`type="email"`, `Your email`, `<textarea`, `Yes`, `No`} {
		if !strings.Contains(h, s) {
			t.Error(s, h)
		}
	}
}
func TestRenderedWordPressFormsAndDroppedMarkup(t *testing.T) {
	for _, tt := range []struct{ name, input, want, absent string }{{"contact", `<form action="https://example.org/wp-admin/admin-post.php"><label>Your email<input type="email" name="email" required></label><input type="hidden" name="_wpnonce" value="a"><textarea name="message"></textarea></form>`, `data-sh-feature="contact"`, `_wpnonce`}, {"search", `<form id="searchform" action="https://example.org"><input name="s"></form>`, `name="q"`, `name="s"`}, {"subscribe", `<form class="jetpack_subscription_widget"><input name="email"></form>`, `data-sh-feature="subscribe"`, ""}, {"scripts", `<p>Keep</p><script src="https://example.org/wp-includes/x.js"></script><div class="sharedaddy">Shares</div><div class="wpl-likebox">Loading...</div>`, `Keep`, `<script`}, {"counter", `<aside class="widget_blog-stats">2,305 hits</aside>`, "", `hits`}} {
		t.Run(tt.name, func(t *testing.T) {
			c := testConverter()
			h := c.cleanContent(tt.input, "/post/")
			if !strings.Contains(h, tt.want) || tt.absent != "" && strings.Contains(h, tt.absent) {
				t.Error(h)
			}
		})
	}
}
func TestMenuBuilding(t *testing.T) {
	for _, tt := range []struct {
		name  string
		items []*item
		want  string
		count int
	}{{"page fallback", []*item{{Type: "page", Title: "B", Path: "/b/", Order: 2}, {Type: "page", Title: "A", Path: "/a/", Order: 1}, {Type: "page", Title: "Child", Path: "/child/", Parent: "1"}}, "A", 2}, {"block navigation", []*item{{Type: "wp_navigation", Status: "publish", Content: `<!-- wp:navigation-link {"label":"Contact","url":"/contact/"} /--><!-- wp:navigation-submenu {"label":"Pages","url":"/about/"} --><!-- wp:navigation-link {"label":"Nested","url":"/nested/"} /--><!-- /wp:navigation-submenu -->`}}, "Contact", 3}} {
		t.Run(tt.name, func(t *testing.T) {
			c := testConverter()
			c.data.Items = tt.items
			c.published = tt.items
			m := c.buildMenu()
			if len(m) == 0 || m[0].Title != tt.want || menuCount(m) != tt.count {
				t.Fatal(m)
			}
			if tt.name == "block navigation" && !strings.Contains(c.menuHTML(m, "/"), "<details>") {
				t.Error("submenu inaccessible")
			}
		})
	}
	b, r := fixture(t, "wordpress-org.xml")
	_ = b
	if !strings.Contains(string(r.Files["index.html"]), "<details><summary>") {
		t.Error("classic nested menu missing")
	}
	c := testConverter()
	for _, v := range []struct{ id, parent, title, group string }{{"1", "2", "First", "primary"}, {"2", "1", "Second", "primary"}, {"3", "0", "Other", "secondary"}} {
		c.data.Items = append(c.data.Items, &item{ID: v.id, Title: v.title, Type: "nav_menu_item", Categories: []category{{Domain: "nav_menu", Slug: v.group}}, Meta: []meta{{Key: "_menu_item_type", Value: "custom"}, {Key: "_menu_item_url", Value: "/"}, {Key: "_menu_item_menu_item_parent", Value: v.parent}}})
	}
	if m := c.buildMenu(); menuCount(m) != 2 {
		t.Error("menu preference/cycle", m)
	}
}

func TestWidgetsJavaScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is unavailable for the browser-helper simulation")
	}
	cmd := exec.Command(node, "widgets_test.js")
	cmd.Stdin = strings.NewReader(featuresJS)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("widget behavior: %v\n%s", err, output)
	}
}
