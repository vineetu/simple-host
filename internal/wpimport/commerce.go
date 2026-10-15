package wpimport

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

func systemWidget(name string) string {
	switch name {
	case "woocommerce_cart", "woocommerce_checkout", "woocommerce_my_account", "woocommerce_order_tracking", "woocommerce/cart", "woocommerce/checkout", "woocommerce/customer-account":
		return FeatureShop
	case "give_receipt", "give_donor_dashboard", "give_profile_editor", "give_login", "give_register", "give/donor-dashboard":
		return FeatureDonation
	}
	if strings.HasPrefix(name, "woocommerce/order-confirmation") {
		return FeatureShop
	}
	return ""
}
func systemPage(i *item) string {
	if i.Type != "page" {
		return ""
	}
	kind := ""
	valid := true
	accept := func(k string) {
		if k == "" || kind != "" && kind != k {
			valid = false
		} else {
			kind = k
		}
	}
	var scan func([]*block)
	scan = func(bs []*block) {
		for _, b := range bs {
			switch {
			case b.Name == "":
				rest := renderShortcodes(b.HTML, func(sc shortcode) string { accept(systemWidget(sc.Tag)); return "" })
				if strings.TrimSpace(rest) != "" {
					valid = false
				}
			case b.Name == "core/shortcode":
				scan(b.Children)
			default:
				accept(systemWidget(b.Name))
			}
		}
	}
	scan(parseBlocks(i.Content))
	if !valid {
		return ""
	}
	return kind
}
func (c *converter) products() []*item {
	var out []*item
	for _, i := range c.published {
		if i.Type == "product" && i.public() {
			out = append(out, i)
		}
	}
	return out
}
func (c *converter) variations(i *item) []*item {
	var out []*item
	for _, v := range c.data.Items {
		if v.Type == "product_variation" && v.Parent == i.ID && v.public() && (v.ID == "" || c.byID[v.ID] == v) {
			out = append(out, v)
		}
	}
	return out
}
func (c *converter) productPrice(i *item) string {
	low, high := "", ""
	var minPrice, maxPrice float64
	for _, v := range c.variations(i) {
		raw := v.metadata("_price")
		price, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		if low == "" || price < minPrice {
			low, minPrice = raw, price
		}
		if high == "" || price > maxPrice {
			high, maxPrice = raw, price
		}
	}
	if low != "" {
		if minPrice == maxPrice {
			return esc(low)
		}
		return "From " + esc(low) + " to " + esc(high)
	}
	for _, t := range i.Categories {
		if t.Domain == "product_type" && t.Slug == "variable" {
			return ""
		}
	}
	price := i.metadata("_price")
	if price == "" {
		price = i.metadata("_sale_price")
	}
	if price == "" {
		price = i.metadata("_regular_price")
	}
	sale, saleErr := strconv.ParseFloat(i.metadata("_sale_price"), 64)
	regular, regularErr := strconv.ParseFloat(i.metadata("_regular_price"), 64)
	if saleErr == nil && regularErr == nil && sale < regular {
		return esc(i.metadata("_sale_price")) + " <span class=\"product-was\">Was " + esc(i.metadata("_regular_price")) + "</span>"
	}
	return esc(price)
}
func optionName(s string) string {
	s = strings.NewReplacer("-", " ", "_", " ").Replace(s)
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return ""
	}
	return string(unicode.ToUpper(r)) + s[n:]
}
func (c *converter) productOptions(i *item) string {
	options := map[string][]string{}
	for _, v := range c.variations(i) {
		for _, m := range v.Meta {
			if !strings.HasPrefix(m.Key, "attribute_") || m.Value == "" {
				continue
			}
			tax := strings.TrimPrefix(m.Key, "attribute_")
			label := optionName(strings.TrimPrefix(tax, "pa_"))
			value := optionName(m.Value)
			if t, ok := c.terms[tax+":"+m.Value]; ok && t.Name != "" {
				value = t.Name
			}
			found := false
			for _, existing := range options[label] {
				found = found || existing == value
			}
			if !found {
				options[label] = append(options[label], value)
			}
		}
	}
	if len(options) == 0 {
		return ""
	}
	s := `<ul class="product-options">`
	for _, label := range sortedKeys(options) {
		s += "<li>" + esc(label+": "+strings.Join(options[label], ", ")) + "</li>"
	}
	return s + "</ul>"
}
func (c *converter) productList(items []*item) string {
	var b strings.Builder
	for _, i := range items {
		if i.Type != "product" || !i.public() {
			continue
		}
		b.WriteString(`<article class="product-list-item">`)
		if thumb := c.byID[i.metadata("_thumbnail_id")]; thumb != nil && thumb.Attachment != "" {
			b.WriteString(`<a href="` + esc(i.Path) + `"><img class="product-thumbnail" src="` + esc(thumb.Attachment) + `" alt="` + esc(thumb.Title) + `" width="120" height="120" loading="lazy" style="object-fit:contain"></a>`)
		}
		b.WriteString(`<h2><a href="` + esc(i.Path) + `">` + esc(i.Title) + `</a></h2><p class="product-price">` + c.productPrice(i) + `</p></article>`)
	}
	return b.String()
}
func productLimit(a map[string]any) int {
	for _, k := range []string{"limit", "perPage", "per_page", "postsToShow", "productsToShow"} {
		if n, err := strconv.Atoi(attrString(a, k)); err == nil && n > 0 {
			return n
		}
	}
	if query, ok := a["query"].(map[string]any); ok {
		return productLimit(query)
	}
	return 8
}
func (c *converter) donation(source, page string) string {
	c.feature(FeatureDonation, source, page, 1)
	return `<p class="sh-donate"><a hidden data-sh-feature="buy" data-product="donate">Donate</a></p>`
}

func (c *converter) dropBlocks(b *block) {
	if b.Name != "" {
		c.result.Report.Dropped["block:"+b.Name]++
	}
	for _, ch := range b.Children {
		c.dropBlocks(ch)
	}
}
func donationIframe(raw, class string) bool {
	for _, name := range strings.Fields(strings.ToLower(class)) {
		if strings.HasPrefix(name, "give-") || strings.HasPrefix(name, "givewp-") {
			return true
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Query().Get("givewp-route") != "" || u.Query().Get("giveDonationFormInIframe") != "" || u.Query().Get("give-form-id") != "" || strings.Contains(u.Path, "/givewp/")
}
