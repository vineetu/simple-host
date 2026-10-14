package wpimport

import (
	"fmt"
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
		ref := attrString(a, "ref")
		if i := c.byID[ref]; i != nil {
			return c.renderContent(i.Content, page, depth+1)
		}
		c.result.Report.Dropped["block:"+name]++
		return ""
	case name == "core/embed" || strings.HasPrefix(name, "core-embed/"):
		u := attrString(a, "url")
		if u == "" {
			u = plain(c.renderBlocks(b.Children, page, depth))
		}
		return embed(u)
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
		if i := c.byID[attrString(a, "ref")]; i != nil {
			return c.menuHTML(navigationItems(parseBlocks(i.Content)), "")
		}
		return c.menuHTML(navigationItems(b.Children), "")
	case name == "core/navigation-link" || name == "core/navigation-submenu":
		return c.menuHTML(navigationItems([]*block{b}), "")
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
					f.Name = strings.TrimPrefix(ch.Name, "jetpack/field-")
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
		return c.widget(FeatureContactForm, "Jetpack form", page, fields)
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
		if i := c.byID[ref]; i != nil && i.Type == "jp_pay_product" {
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
func (c *converter) pluginForm(plugin, id, page string) string {
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
	title := plugin + " " + id
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
		f := c.feature(FeatureContactForm, plugin, page, 0)
		f.Note = "The form definition was not in the export; a name, email and message form was supplied."
	}
	return c.widget(FeatureContactForm, title, page, fs)
}
func (c *converter) shortcode(sc shortcode, page string, depth int) string {
	a := sc.Attrs
	raw := sc.Body
	if raw == "" {
		raw = attrString(a, "0")
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
		return c.widget(FeatureContactForm, "Jetpack form", page, fields)
	case "jetpack_subscription_form", "blog_subscription_form", "mc4wp_form", "mailpoet_form":
		return c.widget(FeatureSubscribe, sc.Tag, page, nil)
	case "gwolle_gb", "gwolle_gb_write", "guestbook", "dmsguestbook", "wpguestbook":
		return c.widget(FeatureGuestbook, "Gwolle guestbook", page, nil)
	case "forum", "wpforo":
		return c.widget(FeatureForum, sc.Tag, page, nil)
	case "jp_blog_stats", "wpstatistics", "post-views", "page-views":
		c.feature(FeatureHitCounter, sc.Tag, page, 1)
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
