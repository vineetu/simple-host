package wpimport

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"
)

type field struct {
	Type, Name, Label string
	Required          bool
	Options           []string
	Value             string
}

func (c *converter) feature(kind, source, page string, count int) *Feature {
	f := c.features[kind]
	if f == nil {
		f = &Feature{Kind: kind, Status: StatusNeedsSetup, Source: source}
		c.features[kind] = f
	}
	if source != "" && !strings.Contains(f.Source, source) {
		f.Source += ", " + source
	}
	found := false
	for _, p := range f.Pages {
		if p == page {
			found = true
		}
	}
	if !found {
		f.Pages = append(f.Pages, page)
	}
	f.Count += count
	return f
}
func (c *converter) configureFeature(f *Feature) {
	resource, sql, preset := "", "", ""
	switch f.Kind {
	case FeatureContactForm:
		resource, preset = "messages", "inbox"
		sql = "CREATE TABLE IF NOT EXISTS messages (id INTEGER PRIMARY KEY, page TEXT, form TEXT, name TEXT, email TEXT, message TEXT, fields TEXT, created_at TEXT)"
		f.Use = "inbox: anyone sends, only the owner reads, and the owner can ask the chat to show messages"
		f.Recipe = "inbox"
	case FeatureSubscribe:
		resource, preset = "subscribers", "inbox"
		sql = "CREATE TABLE IF NOT EXISTS subscribers (id INTEGER PRIMARY KEY, email TEXT NOT NULL, page TEXT, created_at TEXT)"
		f.Use = "an inbox list of email addresses the owner reads; nothing is emailed automatically"
		f.Recipe = "inbox"
	case FeatureComments:
		resource, preset = "comments", "comments"
		sql = "CREATE TABLE IF NOT EXISTS entries (id INTEGER PRIMARY KEY, page TEXT NOT NULL CHECK (length(page) <= 300), name TEXT NOT NULL CHECK (length(name) <= 80), message TEXT NOT NULL CHECK (length(message) <= 1000), created_at TEXT)"
		f.Use = "comments preset: anyone posts after a spam check, no sign-in; approve first if the old site held comments for approval"
		f.Recipe = "comments"
	case FeatureGuestbook:
		resource, preset = "guestbook", "comments"
		f.Moderated = true
		sql = "CREATE TABLE IF NOT EXISTS entries (id INTEGER PRIMARY KEY, name TEXT NOT NULL CHECK (length(name) <= 80), message TEXT NOT NULL CHECK (length(message) <= 1000), created_at TEXT)"
		f.Use = "comments preset with approval on: anyone signs after a spam check, the owner approves each entry"
		f.Recipe = "guestbook"
		f.Note = "Guestbook entries are kept in the plugin's own tables, not in the export, so old entries do not come over."
	case FeatureForum:
		resource, preset = "forum", "wall"
		sql = "CREATE TABLE IF NOT EXISTS posts (id INTEGER PRIMARY KEY, topic INTEGER NOT NULL DEFAULT 0, title TEXT CHECK (length(title) <= 200), name TEXT NOT NULL CHECK (length(name) <= 80), message TEXT NOT NULL CHECK (length(message) <= 4000), created_at TEXT)"
		f.Use = "wall preset: a signed-in discussion; people sign in with an emailed code to post and can delete their own posts"
		f.Recipe = "wall"
	case FeatureSearch:
		f.Status = StatusBuiltIn
		f.Use = "built-in search page made by the importer"
	case FeatureHitCounter:
		f.Status = StatusReplaced
		f.Use = "Simple Host analytics: ask the chat for visitor numbers (site_analytics)"
	case FeatureShop:
		f.Status = StatusNotSupported
		f.Use = "checkout is not supported on Simple Host; products stay as pages, and the owner can add a payment link (Stripe, PayPal, Square) per product"
		f.Note = "The connector updates sh-import/payment-links.json with a product-slug to payment-URL map using update_site keep_other_files. Buy buttons appear when a link is provided."
	}
	if resource != "" {
		body := map[string]any{"kind": "sqlite", "preset": preset}
		if f.Moderated {
			body["approve_first"] = true
		}
		f.Setup = []ToolCall{{Tool: "storage_set_resource", Args: map[string]any{"site": c.in.Site, "name": resource, "body": body}}, {Tool: "storage_sql_schema", Args: map[string]any{"site": c.in.Site, "name": resource, "sql": sql}}}
	}
}
func defaultFields() []field {
	return []field{{Type: "text", Name: "name", Label: "Name", Required: true}, {Type: "email", Name: "email", Label: "Email", Required: true}, {Type: "textarea", Name: "message", Label: "Message", Required: true}}
}
func fieldsHTML(fields []field) string {
	var b strings.Builder
	for n, f := range fields {
		label := f.Label
		if label == "" {
			label = f.Name
		}
		name := f.Name
		if name == "" {
			name = fmt.Sprintf("field_%d", n)
		}
		required := ""
		if f.Required {
			required = " required"
		}
		attrs := ` name="` + esc(name) + `"` + required
		if f.Type == "submit" {
			continue
		}
		b.WriteString("<label>" + esc(label))
		switch f.Type {
		case "textarea":
			b.WriteString("<textarea" + attrs + ` rows="5">` + esc(f.Value) + `</textarea>`)
		case "select":
			b.WriteString("<select" + attrs + ">")
			for _, o := range f.Options {
				b.WriteString(`<option value="` + esc(o) + `">` + esc(o) + `</option>`)
			}
			b.WriteString("</select>")
		case "checkbox", "radio":
			opts := f.Options
			if len(opts) == 0 {
				opts = []string{label}
			}
			for _, o := range opts {
				b.WriteString(`<span><input type="` + f.Type + `"` + attrs + ` value="` + esc(o) + `">` + esc(o) + `</span>`)
			}
		case "acceptance":
			b.WriteString(`<input type="checkbox"` + attrs + ` value="yes">`)
		default:
			t := f.Type
			switch t {
			case "text", "email", "tel", "url", "number", "date":
			default:
				t = "text"
			}
			b.WriteString(`<input type="` + t + `"` + attrs + ` value="` + esc(f.Value) + `">`)
		}
		b.WriteString("</label>")
	}
	return b.String()
}
func (c *converter) widget(kind, source, page string, fields []field) string {
	f := c.feature(kind, source, page, 1)
	data, title, button := "", "", "Send"
	switch kind {
	case FeatureContactForm:
		data, title = "contact", source
		if fields == nil {
			fields = defaultFields()
		}
	case FeatureSubscribe:
		data, title, button = "subscribe", "Subscribe", "Subscribe"
		fields = []field{{Type: "email", Name: "email", Label: "Email", Required: true}}
	case FeatureComments:
		data, title, button = "comments", "Leave a reply", "Post comment"
		fields = []field{{Type: "text", Name: "name", Label: "Name", Required: true}, {Type: "textarea", Name: "message", Label: "Comment", Required: true}}
	case FeatureGuestbook:
		data, title, button = "guestbook", "Guestbook", "Sign"
		f.Moderated = true
		fields = []field{{Type: "text", Name: "name", Label: "Name", Required: true}, {Type: "textarea", Name: "message", Label: "Message", Required: true}}
	case FeatureForum:
		return c.forumWidget(page)
	}
	for _, f := range fields {
		if f.Type == "submit" && f.Label != "" {
			button = f.Label
		}
	}
	moderation := ""
	if kind == FeatureGuestbook || kind == FeatureComments && f.Moderated {
		moderation = ` data-moderated="true"`
	}
	return `<section class="sh-widget" data-sh-feature="` + data + `" data-page="` + esc(page) + `"` + moderation + `><h2>` + esc(title) + `</h2><div data-sh-list></div><form data-sh-feature="` + data + `" data-page="` + esc(page) + `" data-form="` + esc(source) + `"` + moderation + `>` + fieldsHTML(fields) + `<button type="submit">` + button + `</button><p role="status" aria-live="polite"></p></form></section>`
}
func (c *converter) forumWidget(page string) string {
	s := `<section class="sh-widget" data-sh-feature="forum" data-page="` + esc(page) + `"><h2>Discussion</h2><div data-sh-signin></div><div data-sh-topics></div><div data-sh-thread></div><form data-sh-new>` + fieldsHTML([]field{{Type: "text", Name: "name", Label: "Name", Required: true}, {Type: "text", Name: "title", Label: "Topic title", Required: true}, {Type: "textarea", Name: "message", Label: "Message", Required: true}}) + `<button>New topic</button><p role="status"></p></form><form data-sh-reply hidden>` + fieldsHTML([]field{{Type: "text", Name: "name", Label: "Name", Required: true}, {Type: "textarea", Name: "message", Label: "Reply", Required: true}}) + `<button>Reply</button><p role="status"></p></form><p data-sh-error role="status"></p>`
	var earlier strings.Builder
	for _, i := range c.published {
		if i.Type == "topic" {
			earlier.WriteString(`<li><a href="` + esc(i.Path) + `">` + esc(i.Title) + `</a></li>`)
		}
	}
	if earlier.Len() > 0 {
		s += `<h3>Earlier topics</h3><ul>` + earlier.String() + `</ul>`
	}
	return s + "</section>"
}
func (c *converter) comments(i *item) string {
	var b strings.Builder
	count := 0
	moderated := false
	for _, co := range i.Comments {
		if co.Approved == "0" {
			moderated = true
		}
		if co.Approved != "1" || co.Type != "" && co.Type != "comment" {
			continue
		}
		count++
		author := esc(co.Author)
		if u := safeLink(co.URL); u != "" {
			author = `<a href="` + esc(u) + `" rel="nofollow ugc">` + author + `</a>`
		}
		b.WriteString(`<article class="comment"><h3>` + author + `</h3><time>` + esc(co.Date) + `</time><p>` + esc(co.Content) + `</p></article>`)
	}
	if i.CommentStatus != "open" && count == 0 {
		return ""
	}
	c.result.Report.Comments += count
	f := c.feature(FeatureComments, "WordPress comments", i.Path, count)
	f.Moderated = f.Moderated || moderated
	widget := c.widget(FeatureComments, "WordPress comments", i.Path, nil)
	f.Count--
	return `<section class="comments"><h2>Comments</h2>` + b.String() + widget + `</section>`
}
func safeLink(s string) string {
	if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "/") {
		return s
	}
	return ""
}
func (c *converter) product(i *item, page string) string {
	source := "WooCommerce"
	if i.Type == "jp_pay_product" {
		source = "Jetpack payments"
	}
	c.feature(FeatureShop, source, page, 1)
	price := i.metadata("_price")
	if price == "" {
		price = i.metadata("_sale_price")
	}
	if price == "" {
		price = i.metadata("_regular_price")
	}
	slug := i.Slug
	if slug == "" {
		slug = "p-" + i.ID
	}
	return `<p class="product-price">` + esc(price) + `</p><p class="product-sku">` + esc(i.metadata("_sku")) + `</p><a hidden data-sh-feature="buy" data-product="` + esc(slug) + `">Buy</a>`
}

var cfTag = regexp.MustCompile(`\[([a-z]+\*?)\s+([^\]]*)\]`)

func cf7Fields(s string) []field {
	var out []field
	for _, m := range cfTag.FindAllStringSubmatchIndex(s, -1) {
		tag := s[m[2]:m[3]]
		attrs := s[m[4]:m[5]]
		t := strings.TrimSuffix(tag, "*")
		if t == "submit" {
			a := parseAttrs(attrs)
			out = append(out, field{Type: "submit", Label: attrString(a, "0")})
			continue
		}
		switch t {
		case "text", "email", "tel", "url", "number", "date", "textarea", "select", "checkbox", "radio", "acceptance":
		default:
			continue
		}
		parts := strings.Fields(attrs)
		if len(parts) == 0 {
			continue
		}
		name := parts[0]
		a := parseAttrs(attrs[len(name):])
		var opts []string
		for n := 0; ; n++ {
			v, ok := a[fmt.Sprint(n)]
			if !ok {
				break
			}
			str, _ := v.(string)
			if strings.HasPrefix(str, "id:") || strings.HasPrefix(str, "class:") || strings.HasPrefix(str, "default:") || str == "use_label_element" || str == "include_blank" || str == "optional" || str == "placeholder" {
				continue
			}
			opts = append(opts, str)
		}
		start := strings.LastIndex(s[:m[0]], "<label")
		label := ""
		if start >= 0 && start > strings.LastIndex(s[:m[0]], "</label>") {
			label = plain(s[start:m[0]])
		}
		if label == "" {
			prefix := s[:m[0]]
			if n := strings.LastIndex(prefix, "\n"); n >= 0 {
				prefix = prefix[n+1:]
			}
			label = strings.TrimSpace(plain(prefix))
		}
		if t == "acceptance" {
			if end := strings.Index(s[m[1]:], "[/acceptance]"); end >= 0 {
				label = plain(s[m[1] : m[1]+end])
			}
		}
		if label == "" {
			label = name
		}
		f := field{Type: t, Name: name, Label: label, Required: strings.HasSuffix(tag, "*") || t == "acceptance" && !strings.Contains(attrs, "optional"), Options: opts}
		out = append(out, f)
	}
	return out
}
func wpformsFields(s string) []field {
	var data map[string]any
	if json.Unmarshal([]byte(s), &data) != nil {
		return nil
	}
	var raw []any
	switch v := data["fields"].(type) {
	case map[string]any:
		for _, k := range sortedKeys(v) {
			raw = append(raw, v[k])
		}
	case []any:
		raw = v
	}
	var out []field
	for _, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		t := attrString(m, "type")
		if t == "name" {
			t = "text"
		}
		if t == "paragraph-text" {
			t = "textarea"
		}
		f := field{Type: t, Name: "field_" + attrString(m, "id"), Label: attrString(m, "label")}
		f.Required = attrString(m, "required") == "1" || m["required"] == true
		choices, _ := m["choices"].(map[string]any)
		for _, k := range sortedKeys(choices) {
			if a, ok := choices[k].(map[string]any); ok {
				f.Options = append(f.Options, attrString(a, "label"))
			}
		}
		out = append(out, f)
	}
	return out
}
func (c *converter) cleanContent(s, page string) string {
	root := fragment(s)
	walk(root, func(n *xhtml.Node) {
		if n.Parent == nil {
			return
		}
		class := getAttr(n, "class")
		if n.Type == xhtml.ElementNode {
			drop := ""
			if n.Data == "script" || n.Data == "link" || n.Data == "object" || n.Data == "embed" {
				drop = "wordpress_script"
			}
			for _, v := range []string{"sharedaddy", "jp-relatedposts", "wpl-likebox", "jetpack-likes-widget"} {
				if strings.Contains(class, v) {
					drop = v
				}
			}
			if n.Data == "iframe" {
				src := getAttr(n, "src")
				if converted := fragment(embed(src)); converted.FirstChild != nil {
					walk(converted, func(ch *xhtml.Node) {
						if ch.Data == "iframe" {
							setAttr(n, "src", getAttr(ch, "src"))
						}
					})
					src = getAttr(n, "src")
				}
				if !strings.HasPrefix(src, "https://www.youtube-nocookie.com/embed/") && !strings.HasPrefix(src, "https://player.vimeo.com/video/") {
					drop = "external_embed"
				}
			}
			if strings.Contains(class, "widget_blog-stats") || strings.Contains(class, "hit-counter") || strings.Contains(class, "wpstatistics") || strings.Contains(class, "wp-statistics") || strings.Contains(class, "post-views") || strings.Contains(class, "page-views") {
				c.feature(FeatureHitCounter, "WordPress statistics", page, 1)
				drop = "hit_counter"
			}
			if drop != "" {
				c.result.Report.Dropped[drop]++
				n.Parent.RemoveChild(n)
				return
			}
			if n.Data == "form" && !strings.HasPrefix(getAttr(n, "data-sh-feature"), "contact") && getAttr(n, "data-sh-feature") == "" {
				if getAttr(n, "role") == "search" || getAttr(n, "id") == "searchform" || strings.Contains(class, "searchform") {
					c.feature(FeatureSearch, "WordPress search", page, 0)
					replaceNode(n, c.searchForm())
					return
				}
				if hasAncestorClass(n, "jetpack_subscription_widget") {
					replaceNode(n, c.widget(FeatureSubscribe, "Jetpack subscriptions", page, nil))
					return
				}
				action := getAttr(n, "action")
				if c.isOldForm(action) {
					var fs []field
					walk(n, func(ch *xhtml.Node) {
						if ch.Data == "input" || ch.Data == "textarea" || ch.Data == "select" {
							t := getAttr(ch, "type")
							if t == "hidden" || t == "submit" {
								return
							}
							if t == "" {
								t = ch.Data
								if t == "input" {
									t = "text"
								}
							}
							name := getAttr(ch, "name")
							label := name
							if ch.Parent != nil && ch.Parent.Data == "label" {
								label = plain(serialize(ch.Parent))
							}
							f := field{Type: t, Name: name, Label: label, Required: hasAttr(ch, "required"), Value: getAttr(ch, "value")}
							if ch.Data == "select" {
								walk(ch, func(opt *xhtml.Node) {
									if opt.Data == "option" {
										f.Options = append(f.Options, plain(serialize(opt)))
									}
								})
							}
							if t == "checkbox" || t == "radio" {
								f.Options = []string{getAttr(ch, "value")}
							}
							if ch.Data == "textarea" {
								f.Value = plain(serialize(ch))
							}
							fs = append(fs, f)
						}
					})
					replaceNode(n, c.widget(FeatureContactForm, "WordPress form", page, fs))
					return
				}
			}
			rewritten := false
			for _, k := range []string{"href", "src", "poster", "action"} {
				raw := getAttr(n, k)
				if raw == "" {
					continue
				}
				u := c.rewriteURL(raw, page)
				if u != raw {
					setAttr(n, k, u)
					rewritten = true
				}
				remote, _ := url.Parse(raw)
				if k == "src" && remote != nil && remote.Host != "" && u == raw && n.Data != "iframe" {
					if c.mediaPath(raw) == "" {
						delAttr(n, k)
						c.result.Report.Dropped["external_asset"]++
					}
				}
			}
			if rewritten || getAttr(n, "srcset") != "" {
				delAttr(n, "srcset")
				delAttr(n, "sizes")
			}
			for _, a := range append([]xhtml.Attribute(nil), n.Attr...) {
				if strings.HasPrefix(a.Key, "on") {
					delAttr(n, a.Key)
				}
			}
			if style := getAttr(n, "style"); strings.Contains(style, "url(") {
				setAttr(n, "style", c.rewriteInlineCSS(style, page))
			}
			if n.Data == "style" {
				for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
					ch.Data = c.rewriteInlineCSS(ch.Data, page)
				}
			}
		}
		if n.Type == xhtml.TextNode && strings.TrimSpace(n.Data) == "Loading..." {
			c.result.Report.Dropped["wordpress_loading"]++
			n.Parent.RemoveChild(n)
		}
	})
	return serialize(root)
}
func hasAttr(n *xhtml.Node, k string) bool {
	for _, a := range n.Attr {
		if a.Key == k {
			return true
		}
	}
	return false
}
func replaceNode(n *xhtml.Node, s string) {
	root := fragment(s)
	for ch := root.FirstChild; ch != nil; {
		next := ch.NextSibling
		root.RemoveChild(ch)
		n.Parent.InsertBefore(ch, n)
		ch = next
	}
	n.Parent.RemoveChild(n)
}
func (c *converter) isOldForm(action string) bool {
	if action == "" || strings.HasPrefix(action, "/") {
		return true
	}
	for h := range c.hosts {
		if strings.Contains(action, "://"+h+"/") {
			return true
		}
	}
	return false
}

func hasAncestorClass(n *xhtml.Node, class string) bool {
	for ; n != nil; n = n.Parent {
		if strings.Contains(getAttr(n, "class"), class) {
			return true
		}
	}
	return false
}
func (c *converter) rewriteInlineCSS(s, page string) string {
	s = cssImport.ReplaceAllString(s, "")
	return cssURL.ReplaceAllStringFunc(s, func(raw string) string {
		m := cssURL.FindStringSubmatch(raw)
		u := strings.TrimSpace(m[1])
		if strings.HasPrefix(u, "data:") || strings.HasPrefix(u, "#") {
			return raw
		}
		local := c.rewriteURL(u, page)
		if local == u && c.mediaPath(u) != "" {
			return raw
		}
		if local == u && (strings.Contains(u, ":") || strings.HasPrefix(u, "//")) {
			c.result.Report.Dropped["external_asset"]++
			return `url("")`
		}
		return `url("` + local + `")`
	})
}
