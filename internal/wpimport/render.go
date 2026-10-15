package wpimport

import (
	"fmt"
	xhtml "golang.org/x/net/html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var bareEmbed = regexp.MustCompile(`(?m)^\s*(https?://(?:www\.)?(?:youtube\.com/[^\s<]+|youtu\.be/[^\s<]+|vimeo\.com/[^\s<]+))\s*$`)

func embed(raw string) string {
	raw = strings.TrimSpace(raw)
	u, e := url.Parse(raw)
	if e != nil {
		return ""
	}
	host := strings.TrimPrefix(u.Hostname(), "www.")
	id := ""
	src := ""
	switch host {
	case "youtube.com", "youtube-nocookie.com", "m.youtube.com":
		id = u.Query().Get("v")
		if id == "" {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) > 1 && (parts[0] == "embed" || parts[0] == "shorts" || parts[0] == "live") {
				id = parts[1]
			}
		}
		src = "https://www.youtube-nocookie.com/embed/"
	case "youtu.be":
		id = strings.Trim(u.Path, "/")
		src = "https://www.youtube-nocookie.com/embed/"
	case "vimeo.com", "player.vimeo.com":
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		id = parts[len(parts)-1]
		src = "https://player.vimeo.com/video/"
	}
	if id != "" && regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(id) {
		return `<div class="embed"><iframe title="Video" src="` + src + esc(id) + `" loading="lazy" allowfullscreen></iframe></div>`
	}
	if safeLink(raw) == "" {
		return ""
	}
	return `<p><a href="` + esc(raw) + `">` + esc(raw) + `</a></p>`
}

// mapEmbed rebuilds a Google Maps or OpenStreetMap iframe embed with only
// its address kept, or returns "" when raw is not one of those three map
// embed forms.
func mapEmbed(raw string) string {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Scheme != "https" {
		return ""
	}
	ok := false
	switch strings.ToLower(u.Hostname()) {
	case "www.google.com":
		ok = u.Path == "/maps/embed"
	case "maps.google.com":
		ok = u.Path == "/maps" && u.Query().Get("output") == "embed"
	case "www.openstreetmap.org":
		ok = u.Path == "/export/embed.html"
	}
	if !ok {
		return ""
	}
	return `<div class="embed embed-map"><iframe title="Map" src="` + esc(raw) + `" loading="lazy" referrerpolicy="no-referrer-when-downgrade"></iframe></div>`
}
func (c *converter) renderContent(s, page string, depth int) string {
	if depth > 5 {
		c.result.Report.Dropped["block:core/block"]++
		return ""
	}
	if strings.Contains(s, "<!-- wp:") {
		return c.renderBlocks(parseBlocks(s), page, depth)
	}
	s = bareEmbed.ReplaceAllStringFunc(s, func(v string) string { return embed(strings.TrimSpace(v)) })
	var saved []string
	s = renderShortcodes(s, func(sc shortcode) string {
		if !pluginShortcode(sc) {
			return c.shortcode(sc, page, depth)
		}
		rendered := c.shortcode(sc, page, depth)
		saved = append(saved, rendered)
		return "\n\nSH_SHORTCODE_" + fmt.Sprintf("%06d_END", len(saved)-1) + "\n\n"
	})
	s = wpautop(s)
	for n, v := range saved {
		s = strings.ReplaceAll(s, "<p>SH_SHORTCODE_"+fmt.Sprintf("%06d_END", n)+"</p>", v)
		s = strings.ReplaceAll(s, "SH_SHORTCODE_"+fmt.Sprintf("%06d_END", n), v)
	}
	return s
}
func (c *converter) renderBlocks(bs []*block, page string, depth int) string {
	var out strings.Builder
	for _, b := range bs {
		out.WriteString(c.renderBlock(b, page, depth))
	}
	return out.String()
}
func (c *converter) renderBlock(b *block, page string, depth int) string {
	if b.Name == "" {
		return renderShortcodes(b.HTML, func(sc shortcode) string { return c.shortcode(sc, page, depth) })
	}
	name := b.Name
	a := b.Attrs
	switch {
	case name == "core/block":
		if i := c.reusable(attrString(a, "ref"), "wp_block"); i != nil {
			return c.renderContent(i.Content, page, depth+1)
		}
		c.result.Report.Dropped["block:"+name]++
		return ""
	case name == "core/embed" || strings.HasPrefix(name, "core-embed/"):
		u := attrString(a, "url")
		if u == "" {
			u = plain(c.renderBlocks(b.Children, page, depth))
		}
		caption := ""
		walk(fragment(savedBlockHTML(b.Children)), func(n *xhtml.Node) {
			if n.Data == "figcaption" {
				caption += "<figcaption>" + serialize(n) + "</figcaption>"
			}
		})
		return `<figure class="wp-block-embed">` + embed(u) + caption + `</figure>`
	case name == "core/latest-posts" || name == "core/query" || name == "jetpack/top-posts":
		return c.latest()
	case name == "core/archives":
		months := map[string]bool{}
		for _, i := range c.posts {
			if len(i.Date) >= 7 {
				months[i.Date[:7]] = true
			}
		}
		s := "<ul>"
		for _, m := range sortedKeys(months) {
			s += `<li><a href="` + esc(c.postsPath) + `">` + esc(m) + `</a></li>`
		}
		return s + "</ul>"
	case name == "core/categories" || name == "core/tag-cloud":
		taxonomy, prefix := "category", "category"
		if name == "core/tag-cloud" {
			taxonomy, prefix = "post_tag", "tag"
		}
		s := "<ul>"
		for _, k := range sortedKeys(c.terms) {
			t := c.terms[k]
			if t.Taxonomy == taxonomy {
				s += `<li><a href="/` + prefix + `/` + esc(c.termPath(t, map[string]bool{})) + `/">` + esc(t.Name) + `</a></li>`
			}
		}
		return s + "</ul>"
	case name == "core/page-list":
		var ps []*item
		for _, i := range c.published {
			if i.Type == "page" {
				ps = append(ps, i)
			}
		}
		return c.postList(ps)
	case name == "core/navigation":
		m := c.navigationItems(b.Children)
		ref := attrString(a, "ref")
		if i := c.reusable(ref, "wp_navigation"); i != nil {
			m = c.navigationItems(parseBlocks(i.Content))
		}
		if ref != "" && ref == c.headerRef || c.sameMenu(m, c.headerMenu) {
			return ""
		}
		return c.responsiveNav(m, page, "wp-block-navigation", false)
	case name == "core/template-part":
		return ""
	case name == "core/navigation-link" || name == "core/navigation-submenu":
		return c.menuHTML(c.navigationItems([]*block{b}), "")
	case name == "core/shortcode":
		var raw strings.Builder
		for _, ch := range b.Children {
			raw.WriteString(ch.HTML)
		}
		return c.renderContent(raw.String(), page, depth+1)
	case name == "core/search":
		c.feature(FeatureSearch, "WordPress search", page, 0)
		return c.searchForm()
	case name == "core/gallery":
		if ids, ok := a["ids"].([]any); ok {
			var idsS []string
			for _, v := range ids {
				switch v := v.(type) {
				case float64:
					idsS = append(idsS, jsonNumber(v))
				case string:
					idsS = append(idsS, v)
				}
			}
			if len(idsS) > 0 {
				return c.gallery(strings.Join(idsS, ","), attrString(a, "columns"), attrString(a, "sizeSlug"), "")
			}
		}
	case name == "core/site-title":
		return `<a href="/">` + esc(c.data.Title) + `</a>`
	case name == "core/site-tagline":
		return `<p>` + esc(c.data.Description) + `</p>`
	case name == "core/more" || name == "core/nextpage":
		return ""
	case name == "jetpack/contact-form":
		var fields []field
		for _, ch := range b.Children {
			if strings.HasPrefix(ch.Name, "jetpack/field-") {
				t := strings.TrimPrefix(ch.Name, "jetpack/field-")
				if t == "telephone" {
					t = "tel"
				}
				if t == "name" {
					t = "text"
				}
				f := field{Type: t, Name: attrString(ch.Attrs, "id"), Label: attrString(ch.Attrs, "label"), Required: ch.Attrs["required"] == true}
				if f.Name == "" {
					// Jetpack fields often have no id: name them from their
					// label so the saved fields read well and never collide.
					f.Name = fieldSlug(f.Label)
				}
				if f.Name == "" {
					f.Name = strings.TrimPrefix(ch.Name, "jetpack/field-")
				}
				if t == "email" {
					f.Name = "email"
				} else if t == "textarea" && !hasField(fields, "message") {
					f.Name = "message"
				} else if strings.TrimPrefix(ch.Name, "jetpack/field-") == "name" && !hasField(fields, "name") {
					f.Name = "name"
				}
				for base, n := f.Name, 2; hasField(fields, f.Name); n++ {
					f.Name = base + "-" + strconv.Itoa(n)
				}
				if opts, ok := ch.Attrs["options"].([]any); ok {
					for _, v := range opts {
						if s, ok := v.(string); ok {
							f.Options = append(f.Options, s)
						}
					}
				}
				fields = append(fields, f)
			}
		}
		return c.widget(FeatureContactForm, "Jetpack form", page, fields, attrString(a, "title"))
	case name == "wpforms/form-selector":
		return c.pluginForm("wpforms", attrString(a, "formId"), page)
	case name == "jetpack/subscriptions" || strings.Contains(name, "mailpoet"):
		return c.widget(FeatureSubscribe, "Jetpack subscriptions", page, nil)
	case name == "jetpack/blog-stats":
		c.feature(FeatureHitCounter, "Jetpack blog stats", page, 1)
		c.result.Report.Dropped["block:"+name]++
		return ""
	case strings.Contains(name, "gwolle") || strings.Contains(name, "guestbook"):
		return c.widget(FeatureGuestbook, "Gwolle guestbook", page, nil)
	case strings.HasPrefix(name, "woocommerce/") || name == "jetpack/simple-payments" || name == "jetpack/payment-buttons" || name == "jetpack/recurring-payments":
		c.feature(FeatureShop, "WordPress payments", page, 0)
		ref := attrString(a, "productId")
		if ref == "" {
			ref = attrString(a, "product_id")
		}
		if i := c.reusable(ref, "jp_pay_product"); i != nil {
			return `<h2>` + esc(i.Title) + `</h2>` + c.product(i, page) + c.renderContent(i.Content, page, depth+1)
		}
		if strings.HasPrefix(name, "jetpack/") {
			slug := attrString(a, "slug")
			if slug == "" {
				slug = "payment"
				if ref != "" {
					slug += "-" + ref
				}
			}
			return c.renderBlocks(b.Children, page, depth) + `<a hidden data-sh-feature="buy" data-product="` + esc(slug) + `">Buy</a>`
		}
		return c.renderBlocks(b.Children, page, depth) + `<p>Contact the owner to buy.</p>`
	case name == "jetpack/map":
		if u := attrString(a, "mapUrl"); u != "" {
			return `<p><a href="` + esc(u) + `">View map</a></p>`
		}
		return `<p><a href="https://www.openstreetmap.org/">View map</a></p>`
	case name == "core/latest-comments" || name == "core/rss" || name == "core/calendar" || name == "jetpack/related-posts" || name == "jetpack/sharing-buttons" || name == "jetpack/like":
		c.result.Report.Dropped["block:"+name]++
		return ""
	}
	saved := c.renderBlocks(b.Children, page, depth)
	if strings.TrimSpace(saved) == "" {
		c.result.Report.Dropped["block:"+name]++
		return ""
	}
	return saved
}

// reusable returns the item a block points at (a reusable block, a block
// menu, a payment product) only when it has the expected type and is public,
// so a draft or private one never reaches a published page.
func (c *converter) reusable(id, kind string) *item {
	if i := c.byID[strings.TrimSpace(id)]; i != nil && i.Type == kind && i.public() {
		return i
	}
	return nil
}
func (c *converter) gallery(ids, columns, size, kind string) string {
	n, err := strconv.Atoi(columns)
	if err != nil || n < 1 || n > 9 {
		n = 3
	}
	class := "gallery gallery-size-" + size
	if kind == "circle" {
		class += " gallery-circle"
	}
	var b strings.Builder
	b.WriteString(`<div class="` + esc(class) + `" style="--gallery-columns:` + fmt.Sprint(n) + `">`)
	for _, id := range strings.Split(ids, ",") {
		i := c.byID[strings.TrimSpace(id)]
		if i == nil || i.Attachment == "" {
			continue
		}
		b.WriteString(`<figure><a href="` + esc(i.Attachment) + `"><img src="` + esc(i.Attachment) + `" alt="` + esc(i.Title) + `" loading="lazy"></a>`)
		if i.Excerpt != "" {
			b.WriteString("<figcaption>" + esc(plain(i.Excerpt)) + "</figcaption>")
		}
		b.WriteString("</figure>")
	}
	return b.String() + "</div>"
}

// pluginFormName maps a shortcode or block plugin key to the name a
// non-technical owner would recognise.
func pluginFormName(plugin string) string {
	switch plugin {
	case "wpforms":
		return "WPForms"
	case "gravityform":
		return "Gravity Forms"
	case "ninja_form":
		return "Ninja Forms"
	case "formidable":
		return "Formidable Forms"
	}
	return plugin // "Contact Form 7" already reads correctly
}
func (c *converter) pluginForm(plugin, id, page string) string {
	plugin = pluginFormName(plugin)
	i := c.byID[id]
	if i == nil && plugin == "Contact Form 7" {
		for _, candidate := range c.data.Items {
			if candidate.Type == "wpcf7_contact_form" && (candidate.metadata("_hash") == id || candidate.Slug == id) {
				i = candidate
				break
			}
		}
	}
	c.feature(FeatureContactForm, plugin, page, 0)
	var fs []field
	title := ""
	if i != nil {
		title = i.Title
		if plugin == "Contact Form 7" {
			s := i.metadata("_form")
			if s == "" {
				s = i.Content
			}
			fs = cf7Fields(s)
		} else {
			fs = wpformsFields(i.Content)
		}
	}
	if len(fs) == 0 {
		fs = defaultFields()
		if c.formMissing == nil {
			c.formMissing = map[string][]string{}
		}
		known := false
		for _, p := range c.formMissing[plugin] {
			known = known || p == page
		}
		if !known {
			c.formMissing[plugin] = append(c.formMissing[plugin], page)
		}
	}
	return c.widget(FeatureContactForm, plugin, page, fs, title)
}
func (c *converter) shortcode(sc shortcode, page string, depth int) string {
	a := sc.Attrs
	raw := sc.Body
	if raw == "" {
		raw = attrString(a, "0")
	}
	if !pluginShortcode(sc) {
		return preserveUnknown(sc, func(ch shortcode) string { return c.shortcode(ch, page, depth+1) })
	}
	switch sc.Tag {
	case "caption", "wp_caption":
		body := sc.Body
		caption := attrString(a, "caption")
		if caption == "" {
			if end := strings.Index(body, ">"); end >= 0 {
				caption = plain(body[end+1:])
				body = body[:end+1]
			}
		}
		return `<figure class="` + esc(attrString(a, "align")) + `">` + body + `<figcaption>` + esc(caption) + `</figcaption></figure>`
	case "gallery":
		return c.gallery(attrString(a, "ids"), attrString(a, "columns"), attrString(a, "size"), attrString(a, "type"))
	case "embed", "youtube", "vimeo":
		return embed(raw)
	case "audio", "video":
		src := attrString(a, "src")
		if src == "" {
			for _, k := range []string{"mp3", "ogg", "wav", "mp4", "webm"} {
				if src = attrString(a, k); src != "" {
					break
				}
			}
		}
		if src == "" {
			src = raw
		}
		return `<` + sc.Tag + ` controls src="` + esc(src) + `"></` + sc.Tag + `>`
	case "contact-form-7":
		return c.pluginForm("Contact Form 7", attrString(a, "id"), page)
	case "wpforms":
		return c.pluginForm("wpforms", attrString(a, "id"), page)
	case "gravityform", "ninja_form", "formidable":
		return c.pluginForm(sc.Tag, attrString(a, "id"), page)
	case "contact-form":
		var fields []field
		_ = renderShortcodes(sc.Body, func(ch shortcode) string {
			if ch.Tag == "contact-field" {
				t := attrString(ch.Attrs, "type")
				if t == "name" {
					t = "text"
				}
				fields = append(fields, field{Type: t, Name: attrString(ch.Attrs, "label"), Label: attrString(ch.Attrs, "label"), Required: attrString(ch.Attrs, "required") == "1" || attrString(ch.Attrs, "required") == "true", Options: strings.Split(attrString(ch.Attrs, "options"), ",")})
			}
			return ""
		})
		return c.widget(FeatureContactForm, "Jetpack form", page, fields, attrString(a, "title"))
	case "jetpack_subscription_form", "blog_subscription_form", "mc4wp_form", "mailpoet_form":
		return c.widget(FeatureSubscribe, sc.Tag, page, nil)
	case "gwolle_gb", "gwolle_gb_write", "guestbook", "dmsguestbook", "wpguestbook":
		return c.widget(FeatureGuestbook, "Gwolle guestbook", page, nil)
	case "forum", "asgarosforum", "wpforo":
		return c.widget(FeatureForum, sc.Tag, page, nil)
	case "jp_blog_stats", "wpstatistics", "post-views", "page-views", "post_views", "views", "mwr_hit_counter":
		c.feature(FeatureHitCounter, sc.Tag, page, 1)
		c.counterWords(page, plain(strings.Replace(sc.Context, sc.Raw, "", 1)))
		c.result.Report.Dropped["shortcode:"+sc.Tag]++
		return ""
	case "products", "add_to_cart":
		c.feature(FeatureShop, "WooCommerce", page, 0)
		var products []*item
		for _, i := range c.published {
			if i.Type == "product" {
				products = append(products, i)
			}
		}
		return c.postList(products)
	case "playlist":
		c.result.Report.Dropped["shortcode:playlist"]++
		return ""
	}
	if strings.HasPrefix(sc.Tag, "bbp-") {
		return c.widget(FeatureForum, "bbPress", page, nil)
	}
	if strings.Contains(sc.Tag, "mailpoet") {
		return c.widget(FeatureSubscribe, "MailPoet", page, nil)
	}
	c.result.Report.Dropped["shortcode:"+sc.Tag]++
	return renderShortcodes(sc.Body, func(ch shortcode) string { return c.shortcode(ch, page, depth+1) })
}

func savedBlockHTML(bs []*block) string {
	var s strings.Builder
	for _, b := range bs {
		s.WriteString(b.HTML)
		s.WriteString(savedBlockHTML(b.Children))
	}
	return s.String()
}

func hasField(fields []field, name string) bool {
	for _, f := range fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func fieldSlug(label string) string {
	s := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(label), "-"), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	return s
}
