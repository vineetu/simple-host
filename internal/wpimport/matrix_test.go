package wpimport

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
)

// Regression tests from the 2026-10-15 matrix of real WordPress sites
// (restaurant, blog, portfolio, Elementor agency, WooCommerce shop,
// nonprofit, Polylang, theme unit test data, Classic Editor, WordPress.com).

// The theme unit test data's "Template: Password Protected" post is
// published with a post password. WordPress shows only a password prompt, so
// the importer must leave it (and its comments) out like drafts and private
// posts, and say so.
func TestMatrixPrivateContentNeverPublished(t *testing.T) {
	_, r := fixture(t, "matrix-privacy.xml")
	for p, b := range r.Files {
		for _, secret := range []string{"SECRET-PASSWORD", "SECRET-DRAFT", "SECRET-FUTURE", "SECRET-PRIVATE", "SECRET-PENDING", "SECRET-COMMENT", "SECRET-REUSABLE", "SECRET-NAV", "should not be visible"} {
			if strings.Contains(string(b), secret) {
				t.Errorf("%s publishes %s", p, secret)
			}
		}
	}
	for _, p := range []string{"2012/01/template-password-protected/index.html", "members/index.html", "secret-private/index.html", "2027/10/secret-future/index.html", "tag/password-2/index.html"} {
		if _, ok := r.Files[p]; ok && p != "tag/password-2/index.html" {
			t.Errorf("%s was written", p)
		}
	}
	if _, ok := r.Files["2012/01/template-comments/index.html"]; !ok {
		t.Fatal("the public post is missing")
	}
	page := string(r.Files["2012/01/template-comments/index.html"])
	if !strings.Contains(page, "Open daily.") || !strings.Contains(page, ">Comments template</a>") {
		t.Fatalf("the published reusable block or menu link is missing:\n%s", page)
	}
	if r.Report.Posts != 1 || r.Report.Pages != 0 || r.Report.Comments != 1 {
		t.Fatalf("counts: posts %d pages %d comments %d", r.Report.Posts, r.Report.Pages, r.Report.Comments)
	}
	if r.Report.MediaCopied != 0 {
		t.Fatalf("a photo used only by a password-protected post was copied")
	}
	found := false
	for _, w := range r.Report.Warnings {
		found = found || strings.Contains(w, "Left out 2 password-protected")
	}
	if !found {
		t.Fatalf("no plain note about the password-protected items: %v", r.Report.Warnings)
	}
}

func matrixLegBConvert(t *testing.T, items []*item, terms ...term) *Result {
	t.Helper()
	var doc strings.Builder
	doc.WriteString(`<rss><channel><title>Site</title><link>https://example.org</link>`)
	for _, term := range terms {
		b, err := xml.Marshal(term)
		if err != nil {
			t.Fatal(err)
		}
		doc.Write(b)
	}
	for _, i := range items {
		b, err := xml.Marshal(i)
		if err != nil {
			t.Fatal(err)
		}
		doc.Write(b)
	}
	doc.WriteString(`</channel></rss>`)
	r, err := Convert(context.Background(), Input{WXR: []io.Reader{strings.NewReader(doc.String())}, Media: testMedia()})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMatrixSystemPages(t *testing.T) {
	shop := []string{"woocommerce_cart", "woocommerce_checkout", "woocommerce_my_account", "woocommerce_order_tracking"}
	donation := []string{"give_receipt", "give_donor_dashboard", "give_profile_editor", "give_login", "give_register"}
	for kind, tags := range map[string][]string{FeatureShop: shop, FeatureDonation: donation} {
		for _, tag := range tags {
			for _, content := range []string{" \n[" + tag + "] \n", "<!-- wp:shortcode -->[" + tag + " id=1]<!-- /wp:shortcode -->"} {
				if got := systemPage(&item{Type: "page", Content: content}); got != kind {
					t.Errorf("%s: %s", content, got)
				}
			}
		}
	}
	for _, name := range []string{"woocommerce/cart", "woocommerce/checkout", "woocommerce/customer-account", "woocommerce/order-confirmation", "woocommerce/order-confirmation-summary", "give/donor-dashboard"} {
		content := "<!-- wp:" + name + " --><div>Saved widget</div><!-- /wp:" + name + " -->"
		if got := systemPage(&item{Type: "page", Content: content}); got == "" {
			t.Error("system block kept", name)
		}
	}
	for _, content := range []string{"", "We&#039;re sorry, your donation failed to process. Please try again or contact site support.", "Shipping information.\n[woocommerce_cart]", "<!-- wp:paragraph --><p>Keep this</p><!-- /wp:paragraph --><!-- wp:woocommerce/cart /-->"} {
		if got := systemPage(&item{Type: "page", Content: content}); got != "" {
			t.Error("ordinary content omitted", content)
		}
	}
	items := []*item{{ID: "1", Type: "page", Status: "publish", Title: "Home", Link: "https://example.org/", Content: "Welcome"}}
	for n, v := range []struct{ slug, content string }{
		{"cart", `<!-- wp:woocommerce/cart --><div><!-- wp:woocommerce/product-new /--></div><!-- /wp:woocommerce/cart -->`},
		{"checkout", `<!-- wp:woocommerce/checkout /-->`},
		{"my-account", `<!-- wp:shortcode -->[woocommerce_my_account]<!-- /wp:shortcode -->`},
		{"donation-confirmation", `[give_receipt]`},
		{"donor-dashboard", `<!-- wp:give/donor-dashboard {"align":"wide"} /-->`},
	} {
		id := fmt.Sprint(n + 2)
		items = append(items, &item{ID: id, Type: "page", Status: "publish", Title: v.slug, Slug: v.slug, Link: "https://example.org/" + v.slug + "/", Content: v.content})
		items = append(items, &item{ID: fmt.Sprint(n + 20), Type: "nav_menu_item", Status: "publish", Meta: []meta{{"_menu_item_type", "post_type"}, {"_menu_item_object_id", id}}})
	}
	items = append(items, &item{ID: "10", Type: "page", Status: "publish", Title: "Donation failed", Slug: "donation-failed", Content: "Please try again or contact site support."})
	r := matrixLegBConvert(t, items)
	for _, p := range []string{"cart", "checkout", "my-account", "donation-confirmation", "donor-dashboard"} {
		if r.Files[p+"/index.html"] != nil || strings.Contains(string(r.Files["search-index.json"]), p) {
			t.Error("system page published", p)
		}
	}
	if r.Files["donation-failed/index.html"] == nil || r.Report.MenuItems != 2 {
		t.Error("failed page or menu", r.Report)
	}
	want := "Left out the shop's cart, checkout, and account pages (/cart/, /checkout/, /my-account/), because online checkout does not move to Simple Host."
	if len(r.Report.Warnings) != 2 || !slices.Contains(r.Report.Warnings, want) {
		t.Error("warnings", r.Report.Warnings)
	}
	c := testConverter()
	c.byID["2"] = &item{Type: "page", Status: "publish"}
	if m := c.navigationItems(parseBlocks(`<!-- wp:navigation-link {"id":2,"kind":"post-type","url":"https://example.org/cart/","label":"Cart"} /-->`)); len(m) != 0 {
		t.Error("block menu kept omitted page", m)
	}
}

func TestMatrixShopAndProductCategory(t *testing.T) {
	for _, slug := range []string{"shop", "store"} {
		r := matrixLegBConvert(t, []*item{
			{ID: "6", Type: "page", Status: "publish", Title: "Shop", Slug: slug},
			{ID: "37", Type: "product", Status: "publish", Title: "Charcoal Espresso Cup", Link: "https://example.org/product/charcoal-espresso-cup/", Date: "2026-10-15 10:00:00", Meta: []meta{{"_price", "15.00"}, {"_thumbnail_id", "14"}}, Categories: []category{{"product_cat", "mugs", "Mugs"}}},
			{ID: "14", Type: "attachment", Status: "inherit", Title: "Cup", Attachment: "https://example.org/wp-content/uploads/2026/07/photo.png"},
			{ID: "99", Type: "product", Status: "publish", Password: "secret", Title: "SECRET-PRODUCT", Slug: "secret-product", Meta: []meta{{"_product_image_gallery", "16"}}, Categories: []category{{"product_cat", "mugs", "Mugs"}}},
			{ID: "16", Type: "attachment", Status: "inherit", Attachment: "https://example.org/wp-content/uploads/2026/07/second.jpg"},
		})
		for _, p := range []string{slug + "/index.html", "product-category/mugs/index.html"} {
			h := string(r.Files[p])
			for _, want := range []string{`class="product-thumbnail"`, `width="120"`, "Charcoal Espresso Cup</a>", "15.00"} {
				if !strings.Contains(h, want) {
					t.Error(p, "missing", want)
				}
			}
			if strings.Contains(h, "<time") || strings.Contains(h, "SECRET-PRODUCT") {
				t.Error("product list leaks date or private item", p)
			}
		}
		if r.Files["wp-content/uploads/2026/07/photo.png"] == nil || r.Files["wp-content/uploads/2026/07/second.jpg"] != nil {
			t.Error("product photos", sortedKeys(r.Files))
		}
	}
}

func TestMatrixProductDetails(t *testing.T) {
	items := []*item{
		{ID: "49", Type: "product", Status: "publish", Title: "Wheel-Thrown Stoneware Mug", Slug: "wheel-thrown-stoneware-mug", Content: "The long description.", Excerpt: "Our everyday mug.", Meta: []meta{{"_price", "1.00"}, {"_sku", "FC-MUG-WT"}, {"_thumbnail_id", "12"}, {"_product_image_gallery", "13"}}, Categories: []category{{"product_type", "variable", "variable"}}},
		{ID: "50", Type: "product_variation", Parent: "49", Status: "publish", Meta: []meta{{"_price", "28.00"}, {"attribute_pa_glaze", "sage-matte"}, {"attribute_size", "small"}}},
		{ID: "51", Type: "product_variation", Parent: "49", Status: "publish", Meta: []meta{{"_price", "36.00"}, {"attribute_pa_glaze", "oat"}, {"attribute_size", "large"}}},
		{ID: "52", Type: "product_variation", Parent: "49", Status: "publish", Password: "hidden", Title: "SECRET-VARIATION", Meta: []meta{{"_price", "999.00"}, {"attribute_size", "secret-size"}}},
		{ID: "53", Type: "product_variation", Parent: "49", Status: "private", Meta: []meta{{"_price", "0.01"}, {"attribute_size", "private-size"}}},
		{ID: "12", Type: "attachment", Status: "inherit", Title: "Mug", Attachment: "https://example.org/wp-content/uploads/2026/07/photo.png"},
		{ID: "13", Type: "attachment", Status: "inherit", Title: "Another view", Attachment: "https://example.org/wp-content/uploads/2026/07/second.jpg"},
		{ID: "37", Type: "product", Status: "publish", Title: "Charcoal Espresso Cup", Slug: "charcoal-espresso-cup", Meta: []meta{{"_price", "28.00"}, {"_sale_price", "28.00"}, {"_regular_price", "32.00"}, {"_sku", "FC-MUG-ESP"}}},
	}
	r := matrixLegBConvert(t, items, term{Taxonomy: "pa_glaze", Slug: "oat", Name: "Warm Oat"})
	h := string(r.Files["wheel-thrown-stoneware-mug/index.html"])
	for _, want := range []string{"From 28.00 to 36.00", "SKU: FC-MUG-WT", "Our everyday mug.", "Glaze: Sage matte, Warm Oat", "Size: Small, Large", `data-product="wheel-thrown-stoneware-mug"`, `class="gallery`, "Another view"} {
		if !strings.Contains(h, want) {
			t.Error("missing", want, h)
		}
	}
	if strings.Index(h, "Our everyday mug.") > strings.Index(h, "The long description.") || strings.Index(h, `class="featured-image"`) > strings.Index(h, `class="gallery`) {
		t.Error("description or photo order", h)
	}
	if r.Report.MediaCopied != 2 {
		t.Error("gallery photo not copied", r.Report.MediaCopied)
	}
	for p, b := range r.Files {
		for _, secret := range []string{"SECRET-VARIATION", "999.00", "0.01", "secret-size", "private-size"} {
			if strings.Contains(string(b), secret) {
				t.Error(p, "leaks", secret)
			}
		}
	}
	simple := string(r.Files["charcoal-espresso-cup/index.html"])
	if !strings.Contains(simple, `>28.00 <span`) || !strings.Contains(simple, "Was 32.00") || !strings.Contains(simple, "SKU: FC-MUG-ESP") {
		t.Error("simple product", simple)
	}
}

func TestMatrixWooCommerceBlocks(t *testing.T) {
	c := testConverter()
	for n := 0; n < 10; n++ {
		c.published = append(c.published, &item{ID: fmt.Sprint(n), Type: "product", Status: "publish", Title: fmt.Sprint(n), Path: fmt.Sprintf("/product/%d/", n), Meta: []meta{{"_price", "28.00"}}})
	}
	for _, name := range []string{"product-collection", "product-new", "product-best-sellers", "product-on-sale", "product-top-rated", "handpicked-products", "product-category", "all-products", "featured-product"} {
		content := `<!-- wp:woocommerce/` + name + ` --><!-- wp:woocommerce/product-template --><!-- wp:woocommerce/product-image /--><!-- /wp:woocommerce/product-template --><!-- /wp:woocommerce/` + name + ` -->`
		got := c.renderContent(content, "/", 0)
		if strings.Count(got, `class="product-list-item"`) != 8 || strings.Contains(got, "Contact the owner") {
			t.Error(name, got)
		}
	}
	for _, attr := range []string{`{"limit":2}`, `{"perPage":2}`, `{"query":{"perPage":2}}`} {
		got := c.renderContent(`<!-- wp:woocommerce/product-collection `+attr+` /-->`, "/", 0)
		if strings.Count(got, `class="product-list-item"`) != 2 {
			t.Error(attr, got)
		}
	}
	for _, name := range []string{"mini-cart", "customer-account", "cart", "checkout", "breadcrumbs", "store-notices", "catalog-sorting", "product-results-count"} {
		got := c.renderContent(`<!-- wp:woocommerce/`+name+` --><div>Widget text</div><!-- /wp:woocommerce/`+name+` -->`, "/", 0)
		if strings.TrimSpace(got) != "" || c.result.Report.Dropped["block:woocommerce/"+name] != 1 {
			t.Error(name, got, c.result.Report.Dropped)
		}
	}
}

func TestMatrixPostListDates(t *testing.T) {
	c := testConverter()
	for _, typ := range []string{"post", "page", "product", "tribe_events"} {
		h := c.postList([]*item{{Type: typ, Date: "2026-10-15 10:00:00"}})
		if strings.Contains(h, "<time") != (typ == "post") {
			t.Error(typ, h)
		}
	}
}

func TestMatrixDonations(t *testing.T) {
	for _, v := range []struct{ source, content string }{
		{"GiveWP", `[give_form id="11"]`},
		{"GiveWP", `<!-- wp:give/donation-form /-->`},
		{"GiveWP", `<!-- wp:give/form /-->`},
		{"GiveWP", `<iframe src="https://example.org/?givewp-route=donation-form-view&amp;form-id=11"></iframe>`},
		{"Charitable", `[charitable_donation_form]`},
	} {
		r := matrixLegBConvert(t, []*item{{ID: "52", Type: "page", Status: "publish", Title: "Donate", Slug: "donate", Content: "Your gift puts a book in someone's hands.\n\n" + v.content}, {ID: "11", Type: "give_forms", Status: "publish", Title: "SECRET-FORM-DEFINITION", Link: "https://example.org/donations/support-adult-learners/"}})
		h := string(r.Files["donate/index.html"])
		want := `<p class="sh-donate"><a hidden="" data-sh-feature="buy" data-product="donate">Donate</a></p>`
		if !strings.Contains(h, want) || !strings.Contains(h, "Your gift") {
			t.Error("donation markup", h)
		}
		f := featureByKind(r, FeatureDonation)
		if f == nil || f.Source != v.source || f.Status != StatusNotSupported || f.Count != 1 || !slices.Equal(f.Pages, []string{"/donate/"}) || !strings.Contains(f.Note, `putting "donate"`) {
			t.Error("donation feature", f)
		}
		if featureByKind(r, FeatureShop) != nil || r.Files["sh-import/features.js"] == nil || string(r.Files["sh-import/payment-links.json"]) != "{}" {
			t.Error("donation helpers or spurious shop")
		}
		if strings.Contains(string(r.Files["search-index.json"]), "SECRET-FORM-DEFINITION") {
			t.Error("form definition published")
		}
	}
}

func TestMatrixOtherPublicContentTypes(t *testing.T) {
	items := []*item{
		{ID: "1", Type: "page", Status: "publish", Title: "Home", Link: "https://example.org/", Content: `<a href="https://example.org/events/">Events</a><a href="https://example.org/event/annual-gala-turning-the-page/">Gala</a>`},
		{ID: "24", Type: "tribe_events", Status: "publish", Title: "Annual gala", Link: "https://example.org/event/annual-gala-turning-the-page/", CommentStatus: "open", Content: "Come to the gala.", Meta: []meta{{"_EventStartDate", "2026-12-05 18:30:00"}, {"_EventEndDate", "2026-12-05 22:00:00"}, {"_EventVenueID", "16"}, {"_EventOrganizerID", "20"}}},
		{ID: "21", Type: "tribe_events", Status: "publish", Title: "Tutor training", Link: "https://example.org/event/tutor-training/", Content: `<img src="https://example.org/wp-content/uploads/2026/07/photo.png">`, Meta: []meta{{"_EventStartDate", "2026-10-24 09:00:00"}, {"_EventEndDate", "2026-10-24 12:00:00"}}, Comments: []comment{{Approved: "1", Author: "Ada", Content: "I will attend."}}},
		{ID: "16", Type: "tribe_venue", Status: "publish", Title: "Claremont Club & Spa", Link: "https://example.org/venue/claremont-club-spa/", Meta: []meta{{"_VenueAddress", "41 Tunnel Rd"}, {"_VenueCity", "Berkeley"}}},
		{ID: "20", Type: "tribe_organizer", Status: "publish", Title: "Development Office", Link: "https://example.org/organizer/development-office/"},
		{ID: "90", Type: "book", Status: "publish", Title: "Book", Link: "https://example.org/books/a-book/"},
		{ID: "91", Type: "book", Status: "publish", Password: "hidden", Title: "SECRET-BOOK", Link: "https://example.org/books/secret-book/", Content: `<img src="https://example.org/wp-content/uploads/2026/07/second.jpg">`},
		{ID: "92", Type: "tribe_events", Status: "draft", Title: "SECRET-EVENT", Link: "https://example.org/event/secret-event/", Content: `<img src="https://example.org/wp-content/uploads/2026/07/second.jpg">`},
		{ID: "93", Type: "book", Status: "publish", Title: "SECRET-PLAIN", Link: "https://example.org/?post_type=book&p=93", Content: `<img src="https://example.org/wp-content/uploads/2026/07/second.jpg">`},
		{ID: "94", Type: "book", Status: "publish", Title: "SECRET-FOREIGN", Link: "https://elsewhere.test/books/book/", Content: `<img src="https://example.org/wp-content/uploads/2026/07/second.jpg">`},
		{ID: "95", Type: "give_forms", Status: "publish", Title: "SECRET-GIVE", Link: "https://example.org/donations/form/"},
		{ID: "96", Type: "wp_custom_record", Status: "publish", Title: "SECRET-INTERNAL", Link: "https://example.org/records/record/"},
		{ID: "97", Type: "acf-custom", Status: "publish", Title: "SECRET-ACF", Link: "https://example.org/fields/field/"},
		{ID: "98", Type: "_private_record", Status: "publish", Title: "SECRET-UNDERSCORE", Link: "https://example.org/records/private/"},
		{ID: "100", Type: "elementor_library", Status: "publish", Title: "SECRET-TEMPLATE", Link: "https://example.org/template/card/"},
		{ID: "101", Type: "product_variation", Status: "publish", Title: "SECRET-ORPHAN-VARIATION", Link: "https://example.org/product/variation/"},
		{ID: "102", Type: "nav_menu_item", Status: "publish", Title: "Events", Meta: []meta{{"_menu_item_type", "custom"}, {"_menu_item_url", "https://example.org/events/"}}},
		{ID: "103", Type: "nav_menu_item", Status: "publish", Title: "Annual gala", Meta: []meta{{"_menu_item_type", "post_type"}, {"_menu_item_object_id", "24"}}},
		{ID: "104", Type: "nav_menu_item", Status: "publish", Title: "SECRET-MENU", Meta: []meta{{"_menu_item_type", "post_type"}, {"_menu_item_object_id", "91"}}},
	}
	r := matrixLegBConvert(t, items)
	if r.Report.OtherItems != 5 || r.Report.Pages != 1 || r.Report.Comments != 1 || r.Report.MenuItems != 2 {
		t.Error("counts", r.Report)
	}
	for _, p := range []string{"event/annual-gala-turning-the-page/index.html", "event/tutor-training/index.html", "venue/claremont-club-spa/index.html", "organizer/development-office/index.html", "books/a-book/index.html", "events/index.html"} {
		if r.Files[p] == nil {
			t.Error("missing public item", p)
		}
	}
	gala := string(r.Files["event/annual-gala-turning-the-page/index.html"])
	for _, want := range []string{"When:</strong> Saturday 5 December 2026, 18:30 to 22:00", "Where:</strong> Claremont Club &amp; Spa, 41 Tunnel Rd, Berkeley", "Organizer:</strong> Development Office", `data-sh-feature="comments"`, `href="../../events/"`} {
		if !strings.Contains(gala, want) {
			t.Error("event detail", want, gala)
		}
	}
	training := string(r.Files["event/tutor-training/index.html"])
	if !strings.Contains(training, "I will attend.") || !strings.Contains(training, `src="../../wp-content/uploads/2026/07/photo.png"`) {
		t.Error("event comments/photo", training)
	}
	if strings.Contains(string(r.Files["venue/claremont-club-spa/index.html"]), `data-sh-feature="comments"`) {
		t.Error("closed venue has reply form")
	}
	listing := string(r.Files["events/index.html"])
	if strings.LastIndex(listing, ">Tutor training</a>") > strings.LastIndex(listing, ">Annual gala</a>") || !strings.Contains(listing, "Saturday 24 October 2026, 09:00 to 12:00") {
		t.Error("event list order/date", listing)
	}
	if !strings.Contains(string(r.Files["index.html"]), `href="events/"`) || !strings.Contains(string(r.Files["index.html"]), `href="event/annual-gala-turning-the-page/"`) {
		t.Error("old event links not rewritten")
	}
	if !strings.Contains(string(r.Files["search-index.json"]), "/books/a-book/") || !strings.Contains(string(r.Files["search-index.json"]), "/event/tutor-training/") {
		t.Error("new items absent in search")
	}
	if r.Files["wp-content/uploads/2026/07/second.jpg"] != nil {
		t.Error("photo from excluded content copied")
	}
	for p, b := range r.Files {
		if strings.Contains(string(b), "SECRET-") {
			t.Error(p, "publishes hidden content")
		}
	}
}

func TestMatrixEventsPageAndPrivateVenue(t *testing.T) {
	r := matrixLegBConvert(t, []*item{
		{ID: "1", Type: "page", Status: "publish", Title: "Community events", Slug: "events", Content: "Join our community events."},
		{ID: "2", Type: "tribe_events", Status: "publish", Title: "Book drive", Link: "https://example.org/event/book-drive/", Meta: []meta{{"_EventStartDate", "2026-11-01 00:00:00"}, {"_EventEndDate", "2026-11-02 23:59:59"}, {"_EventAllDay", "yes"}, {"_EventVenueID", "3"}, {"_EventOrganizerID", "4"}}},
		{ID: "3", Type: "tribe_venue", Status: "publish", Password: "secret", Title: "SECRET-VENUE", Link: "https://example.org/venue/private/", Meta: []meta{{"_VenueAddress", "SECRET-ADDRESS"}}},
		{ID: "4", Type: "tribe_organizer", Status: "private", Title: "SECRET-ORGANIZER", Link: "https://example.org/organizer/private/"},
	})
	h := string(r.Files["events/index.html"])
	if !strings.Contains(h, "Join our community events.") || !strings.Contains(h, ">Book drive</a>") || !strings.Contains(h, "Sunday 1 November 2026 to Monday 2 November 2026, all day") {
		t.Error("events page not retained/extended", h)
	}
	event := string(r.Files["event/book-drive/index.html"])
	if strings.Contains(event, "Where:") || strings.Contains(event, "Organizer:") || strings.Contains(event, "00:00") {
		t.Error("private reference or all-day clock", event)
	}
	for p, b := range r.Files {
		if strings.Contains(string(b), "SECRET-") {
			t.Error(p, "leaks private reference")
		}
	}
	if r.Report.OtherItems != 1 {
		t.Error("private event reference counted", r.Report.OtherItems)
	}
	c := testConverter()
	for _, link := range []string{"", "https://example.org/", "https://example.org/?p=9", "https://example.org/book/?post_type=book", "https://elsewhere.test/book/"} {
		if c.carries(&item{Type: "book", Link: link}) {
			t.Error("invalid public permalink accepted", link)
		}
	}
	if got := eventWhen(&item{Meta: []meta{{"_EventStartDate", "2026-11-01 00:00:00"}, {"_EventEndDate", "2026-11-01 23:59:59"}, {"_EventAllDay", "yes"}}}); got != "Sunday 1 November 2026, all day" {
		t.Error("single all-day event", got)
	}
}
