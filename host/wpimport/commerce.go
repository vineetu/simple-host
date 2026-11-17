package wpimport

import (
	"net/url"
	"sort"
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
	if c.variationsByParent == nil {
		c.variationsByParent = map[string][]*item{}
		for _, v := range c.data.Items {
			if v.Type == "product_variation" && v.public() && (v.ID == "" || c.byID[v.ID] == v) {
				c.variationsByParent[v.Parent] = append(c.variationsByParent[v.Parent], v)
			}
		}
	}
	return c.variationsByParent[i.ID]
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
	if activeSale(i) {
		return esc(i.metadata("_sale_price")) + " <span class=\"product-was\">Was " + esc(i.metadata("_regular_price")) + "</span>"
	}
	return esc(price)
}
func activeSale(i *item) bool {
	sale, saleErr := strconv.ParseFloat(i.metadata("_sale_price"), 64)
	regular, regularErr := strconv.ParseFloat(i.metadata("_regular_price"), 64)
	return i.metadata("_price") == i.metadata("_sale_price") && saleErr == nil && regularErr == nil && sale < regular
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
		if thumb := c.attachment(i.metadata("_thumbnail_id")); thumb != nil && thumb.Attachment != "" {
			b.WriteString(`<a href="` + esc(i.Path) + `"><img class="product-thumbnail" src="` + esc(thumb.Attachment) + `" alt="` + esc(thumb.Title) + `" width="120" height="120" loading="lazy" style="object-fit:contain"></a>`)
		}
		b.WriteString(`<h2><a href="` + esc(i.Path) + `">` + esc(i.Title) + `</a></h2><p class="product-price">` + c.productPrice(i) + `</p></article>`)
	}
	return b.String()
}

// blockProducts keeps a block's selection, or declines it when the export
// does not contain enough information to select the same products.
func (c *converter) blockProducts(name string, a map[string]any) ([]*item, bool) {
	switch name {
	case "woocommerce/featured-product":
		if attrString(a, "productId") == "" {
			return nil, false
		}
	case "woocommerce/handpicked-products":
		if _, ok := a["products"]; !ok {
			return nil, false
		}
	case "woocommerce/product-category":
		if _, ok := a["categories"]; !ok {
			return nil, false
		}
	case "woocommerce/product-on-sale", "woocommerce/product-new", "woocommerce/all-products", "woocommerce/product-collection":
	default:
		return nil, false
	}
	query := a
	if q, ok := a["query"].(map[string]any); ok {
		query = q
	}
	// These collections depend on purchases, ratings, or the current product.
	if collection := attrString(a, "collection"); collection != "" && collection != "woocommerce/product-collection/all" && collection != "woocommerce/product-collection/on-sale" && collection != "woocommerce/product-collection/new-arrivals" && collection != "woocommerce/product-collection/handpicked" {
		return nil, false
	}
	for _, key := range []string{"search", "woocommerceAttributes", "woocommerceStockStatus", "taxQuery", "featured", "inherit"} {
		v := query[key]
		switch v := v.(type) {
		case nil:
		case bool:
			if v {
				return nil, false
			}
		case string:
			if v != "" {
				return nil, false
			}
		case []any:
			if len(v) > 0 {
				return nil, false
			}
		case map[string]any:
			if len(v) > 0 {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	values := func(v any) ([]string, bool) {
		var out []string
		switch v := v.(type) {
		case []any:
			for _, id := range v {
				s := attrString(map[string]any{"id": id}, "id")
				if s == "" {
					return nil, false
				}
				out = append(out, s)
			}
		case string:
			for _, id := range strings.Split(v, ",") {
				if id = strings.TrimSpace(id); id != "" {
					out = append(out, id)
				}
			}
		default:
			return nil, false
		}
		return out, true
	}
	products := c.products()
	ids, selected := a["products"]
	if id := attrString(a, "productId"); id != "" {
		ids, selected = []any{id}, true
	}
	if raw, exists := query["woocommerceHandPickedProducts"]; exists {
		handpicked, ok := raw.([]any)
		if !ok {
			return nil, false
		}
		if len(handpicked) > 0 {
			ids, selected = handpicked, true
		}
	}
	if attrString(a, "collection") == "woocommerce/product-collection/handpicked" && !selected {
		return nil, false
	}
	if selected {
		wanted, ok := values(ids)
		if !ok || len(wanted) == 0 {
			return nil, false
		}
		byID := map[string]*item{}
		for _, i := range products {
			byID[i.ID] = i
		}
		products = nil
		for _, id := range wanted {
			if i := byID[id]; i != nil {
				products = append(products, i)
			} else {
				return nil, false
			}
		}
	}
	if cats, restricted := a["categories"]; restricted {
		wanted, ok := values(cats)
		if !ok || len(wanted) == 0 {
			return nil, false
		}
		allowed := map[string]bool{}
		for _, value := range wanted {
			matched := false
			for _, t := range c.terms {
				if t.Taxonomy == "product_cat" && (t.Slug == value || t.ID == value) {
					allowed[t.Slug] = true
					matched = true
				}
			}
			// Item-derived categories need no channel-level declaration.
			for _, i := range products {
				for _, cat := range i.Categories {
					if cat.Domain == "product_cat" && cat.Slug == value {
						allowed[cat.Slug] = true
						matched = true
					}
				}
			}
			if !matched {
				return nil, false
			}
		}
		var filtered []*item
		for _, i := range products {
			for _, cat := range i.Categories {
				if cat.Domain == "product_cat" && allowed[cat.Slug] {
					filtered = append(filtered, i)
					break
				}
			}
		}
		products = filtered
	}
	if name == "woocommerce/product-on-sale" || query["woocommerceOnSale"] == true || attrString(a, "collection") == "woocommerce/product-collection/on-sale" {
		var filtered []*item
		for _, i := range products {
			onSale := activeSale(i)
			for _, v := range c.variations(i) {
				onSale = onSale || activeSale(v)
			}
			if onSale {
				filtered = append(filtered, i)
			}
		}
		products = filtered
	}
	if excluded, ok := query["exclude"]; ok {
		ids, valid := values(excluded)
		if !valid {
			return nil, false
		}
		var filtered []*item
		for _, i := range products {
			keep := true
			for _, id := range ids {
				keep = keep && i.ID != id
			}
			if keep {
				filtered = append(filtered, i)
			}
		}
		products = filtered
	}
	orderBy := attrString(query, "orderBy")
	if orderBy == "" {
		orderBy = attrString(query, "orderby")
	}
	if orderBy == "" && (name == "woocommerce/product-new" || attrString(a, "collection") == "woocommerce/product-collection/new-arrivals") {
		orderBy = "date"
	}
	order := strings.ToLower(attrString(query, "order"))
	if order != "" && order != "asc" && order != "desc" {
		return nil, false
	}
	if orderBy == "" && order != "" {
		return nil, false
	}
	if orderBy != "" {
		if orderBy != "date" && orderBy != "title" && orderBy != "menu_order" && orderBy != "id" {
			return nil, false
		}
		products = append([]*item(nil), products...)
		sort.SliceStable(products, func(a, b int) bool {
			i, j := products[a], products[b]
			if order == "desc" || order == "" && orderBy == "date" {
				i, j = j, i
			}
			switch orderBy {
			case "date":
				return i.Date < j.Date
			case "title":
				return i.Title < j.Title
			case "menu_order":
				return i.Order < j.Order
			default:
				x, _ := strconv.Atoi(i.ID)
				y, _ := strconv.Atoi(j.ID)
				return x < y
			}
		})
	}
	offset := 0
	if raw := attrString(query, "offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return nil, false
		}
		offset = min(n, len(products))
	}
	products = products[offset:]
	return products[:min(len(products), productLimit(a))], true
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
