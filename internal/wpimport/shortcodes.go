package wpimport

import (
	"regexp"
	"strings"
)

type shortcode struct {
	Tag     string
	Attrs   map[string]any
	Body    string
	Raw     string
	Context string
}

var shortOpen = regexp.MustCompile(`^\[([a-z0-9_-]+)(\s[^\]]*|/?)\]`)
var shortAttrs = regexp.MustCompile(`([a-zA-Z0-9_-]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s\]]+))|"([^"]*)"|'([^']*)'|([^\s]+)`)

func parseAttrs(s string) map[string]any {
	a := map[string]any{}
	n := 0
	for _, m := range shortAttrs.FindAllStringSubmatch(strings.TrimSpace(strings.TrimSuffix(s, "/")), -1) {
		if m[1] != "" {
			v := m[2]
			if m[3] != "" {
				v = m[3]
			}
			if m[4] != "" {
				v = m[4]
			}
			a[strings.ToLower(m[1])] = v
		} else {
			v := m[5]
			if m[6] != "" {
				v = m[6]
			}
			if m[7] != "" {
				v = m[7]
			}
			a[jsonNumber(float64(n))] = v
			n++
		}
	}
	return a
}
func renderShortcodes(s string, render func(shortcode) string) string {
	var out strings.Builder
	for p := 0; p < len(s); {
		if s[p] != '[' {
			out.WriteByte(s[p])
			p++
			continue
		}
		if strings.HasPrefix(s[p:], "[[") {
			if e := strings.Index(s[p+2:], "]]"); e >= 0 {
				out.WriteString(s[p+1 : p+2+e+1])
				p += e + 4
				continue
			}
		}
		m := shortOpen.FindStringSubmatch(s[p:])
		if m == nil {
			out.WriteByte(s[p])
			p++
			continue
		}
		end := p + len(m[0])
		body := ""
		if !strings.HasSuffix(strings.TrimSpace(m[2]), "/") {
			close := "[/" + m[1] + "]"
			cursor := end
			depth := 1
			for cursor < len(s) {
				nextClose := strings.Index(s[cursor:], close)
				if nextClose < 0 {
					break
				}
				ci := cursor + nextClose
				nextOpen := strings.Index(s[cursor:ci], "["+m[1])
				if nextOpen >= 0 {
					oi := cursor + nextOpen
					if nested := shortOpen.FindStringSubmatch(s[oi:]); nested != nil && nested[1] == m[1] && !strings.HasSuffix(strings.TrimSpace(nested[2]), "/") {
						depth++
						cursor = oi + len(nested[0])
						continue
					}
				}
				depth--
				if depth == 0 {
					body = s[end:ci]
					end = ci + len(close)
					break
				}
				cursor = ci + len(close)
			}
		}
		out.WriteString(render(shortcode{Tag: m[1], Attrs: parseAttrs(m[2]), Body: body, Raw: s[p:end], Context: s[strings.LastIndex(s[:p], "\n")+1 : end+lineEnd(s[end:])]}))
		p = end
	}
	return out.String()
}

// Unregistered shortcodes are ordinary text in WordPress.
func pluginShortcode(sc shortcode) bool {
	for _, prefix := range []string{"et_pb_", "vc_", "su_", "bbp-", "woocommerce_", "jetpack_", "mailpoet_", "wpstatistics", "elementor", "wpforms_", "gravityforms_", "ninja_", "formidable_", "mc4wp_", "mailchimp_", "gwolle_", "jetpack-"} {
		if strings.HasPrefix(sc.Tag, prefix) {
			return true
		}
	}
	switch sc.Tag {
	case "give_form", "give_donation_form", "give_receipt", "give_donor_dashboard", "give_profile_editor", "give_login", "give_register", "charitable_donation_form", "donation_form", "donate", "donate_button",
		"caption", "wp_caption", "gallery", "embed", "audio", "video", "youtube", "vimeo", "playlist",
		"contact-form-7", "contact-form", "contact-field", "wpforms", "gravityform", "gravityforms", "ninja_form", "ninja_forms", "formidable",
		"blog_subscription_form", "mc4wp_form", "mailchimp", "mailchimp_form", "mc4wp", "mailpoet", "gwolle_gb", "gwolle_gb_write", "guestbook", "dmsguestbook", "wpguestbook",
		"forum", "asgarosforum", "wpforo", "jp_blog_stats", "post-views", "page-views", "post_views", "post_views_counter", "pvc_post_views", "hit_counter", "views_count", "wp_statistics", "views", "mwr_hit_counter", "wysija_form",
		"products", "product", "product_page", "product_category", "product_categories", "add_to_cart", "add_to_cart_url", "woocommerce_cart", "woocommerce_checkout", "woocommerce_my_account", "woocommerce_order_tracking", "recent_products", "featured_products", "sale_products", "best_selling_products", "top_rated_products", "related_products":
		return true
	}
	// WordPress itself never shows a bare tag with an underscore as text: it
	// is always a shortcode, registered or not.
	return strings.Contains(sc.Tag, "_")
}

func lineEnd(s string) int {
	if n := strings.IndexByte(s, '\n'); n >= 0 {
		return n
	}
	return len(s)
}

// Preserve the unknown wrapper while registered shortcodes inside still run.
func preserveUnknown(sc shortcode, render func(shortcode) string) string {
	if sc.Body == "" {
		return sc.Raw
	}
	close := "[/" + sc.Tag + "]"
	openEnd := len(sc.Raw) - len(sc.Body) - len(close)
	return sc.Raw[:openEnd] + renderShortcodes(sc.Body, render) + close
}
