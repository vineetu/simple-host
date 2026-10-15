package wpimport

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
	"time"
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

func encodeTestJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			im.Set(x, y, color.NRGBA{uint8(x * 5), uint8(y * 5), 120, 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, im, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// insertExifOrientation inserts a minimal, well-formed APP1 Exif segment
// carrying the given Orientation tag right after a JPEG's SOI marker.
func insertExifOrientation(jpg []byte, orientation byte) []byte {
	tiff := []byte{
		'I', 'I', 0x2A, 0x00, // little-endian TIFF header
		0x08, 0x00, 0x00, 0x00, // first IFD at offset 8
		0x01, 0x00, // one directory entry
		0x12, 0x01, // tag 0x0112, Orientation
		0x03, 0x00, // type 3, SHORT
		0x01, 0x00, 0x00, 0x00, // one value
		orientation, 0x00, 0x00, 0x00,
	}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	size := len(payload) + 2
	app1 := append([]byte{0xFF, 0xE1, byte(size >> 8), byte(size)}, payload...)
	out := append([]byte{}, jpg[:2]...)
	out = append(out, app1...)
	return append(out, jpg[2:]...)
}

// s1 (the restaurant on Astra) has a kitchen photo taken sideways on a
// phone: a JPEG whose Exif Orientation tag is 6. shrinkImage must read that
// tag itself, rotate the pixels to match, and never emit the old "EXIF
// orientation was not applied" warning; a corrupt or truncated Exif block
// must never panic the importer.
func TestMatrixPhotoOrientation(t *testing.T) {
	base := encodeTestJPEG(t, 40, 20)
	rotated := insertExifOrientation(base, 6)
	l := DefaultLimits
	out, note := shrinkImage(rotated, l)
	if note != "" {
		t.Fatalf("the EXIF-orientation warning must be gone, got %q", note)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 20 || cfg.Height != 40 {
		t.Fatalf("not rotated to match orientation 6: got %dx%d, want 20x40", cfg.Width, cfg.Height)
	}
	// Truncating the file at every possible length, inside the JPEG markers
	// and inside the Exif TIFF data, must never panic.
	for n := 0; n <= len(rotated); n++ {
		func(b []byte) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panicked on a %d-byte prefix of the photo: %v", len(b), r)
				}
			}()
			jpegOrientation(b)
			shrinkImage(b, l)
		}(rotated[:n])
	}
}

// A2: per-file photo warnings used to flood the list the person reads (one
// line per skipped or oversized photo). They must collapse into one line
// per kind, with a count and up to five example file names, while the
// machine-readable MediaMissing list keeps every file.
func TestMatrixMediaWarningAggregation(t *testing.T) {
	c := testConverter()
	big := append(append([]byte(nil), testPNG(t)...), make([]byte, 2<<20)...)
	l := DefaultLimits
	l.MaxImagePixels = 1 // the tiny test photo already "exceeds" this
	names := []string{"a.png", "b.png", "c.png", "d.png", "e.png", "f.png"}
	for _, name := range names {
		b, note := shrinkImage(big, l)
		if b != nil {
			t.Fatalf("%s: expected the oversized photo to be skipped", name)
		}
		c.noteMedia(note, "wp-content/uploads/2026/07/"+name)
		c.result.Report.MediaMissing = append(c.result.Report.MediaMissing, "https://example.org/wp-content/uploads/2026/07/"+name)
	}
	c.flushMediaNotes()
	if len(c.result.Report.Warnings) != 1 {
		t.Fatalf("want one aggregated warning line, got %v", c.result.Report.Warnings)
	}
	w := c.result.Report.Warnings[0]
	if !strings.Contains(w, "Left out 6 photos") || !strings.Contains(w, "a.png") || !strings.Contains(w, "e.png") {
		t.Fatalf("aggregated warning missing count or examples: %q", w)
	}
	if strings.Contains(w, "f.png") {
		t.Fatalf("aggregated warning should cap at 5 examples: %q", w)
	}
	if len(c.result.Report.MediaMissing) != len(names) {
		t.Fatalf("the full machine-readable list must keep every file, got %v", c.result.Report.MediaMissing)
	}

	c2 := testConverter()
	c2.limits.MaxSiteBytes = 50 << 20
	c2.noteMedia("budget_skip", "wp-content/uploads/x.jpg")
	c2.noteMedia("budget_skip", "wp-content/uploads/y.jpg")
	c2.flushMediaNotes()
	found := false
	for _, w := range c2.result.Report.Warnings {
		found = found || strings.Contains(w, "Left out 2 photos to fit the 50 MB site limit") && strings.Contains(w, "x.jpg") && strings.Contains(w, "y.jpg")
	}
	if !found {
		t.Fatalf("size-limit wording must be accurate to Limits.MaxSiteBytes: %v", c2.result.Report.Warnings)
	}
}

// A3: s1's Reservations page uses WPForms, whose form fields never appear in
// a WordPress export. The importer used to title the stand-in form
// "wpforms 28" and report Source as "wpforms, wpforms 28"; it must instead
// name the real plugin, show no heading when the real title is unknown, and
// explain itself in the feature Note. Contact Form 7, whose definition IS
// in the export, keeps its real title as a heading.
func TestMatrixFormPluginsWithoutDefinitions(t *testing.T) {
	_, r := fixture(t, "matrix-form-plugins.xml")
	f := featureByKind(r, FeatureContactForm)
	if f == nil {
		t.Fatal("contact form feature missing")
	}
	if !strings.Contains(f.Source, "WPForms") || !strings.Contains(f.Source, "Contact Form 7") {
		t.Fatalf("source should name the real plugins: %q", f.Source)
	}
	if strings.Contains(f.Source, "wpforms") || strings.Contains(f.Source, " 28") {
		t.Fatalf("source still shows the raw plugin-and-id bug: %q", f.Source)
	}
	if !strings.Contains(f.Note, "WPForms keeps its forms out of the WordPress export") || !strings.Contains(f.Note, "/reservations/") {
		t.Fatalf("note missing or wrong: %q", f.Note)
	}
	reservations := string(r.Files["reservations/index.html"])
	if strings.Contains(reservations, "wpforms 28") || strings.Contains(reservations, "<h2>WPForms</h2>") {
		t.Fatalf("no heading should appear when the real title is unknown: %s", reservations)
	}
	contact := string(r.Files["contact/index.html"])
	if !strings.Contains(contact, "<h2>Contact form 1</h2>") {
		t.Fatalf("the real CF7 title should still head the form when its definition is in the export: %s", contact)
	}
}

// A4: s1's Hours & Location page embeds a Google Maps iframe, which used to
// be dropped like any other external embed. It must be kept, rebuilt down
// to just its address, and the aspect ratio must be map-shaped (4:3), not
// video-shaped (16:9); an unrelated iframe is still dropped, now with one
// plain warning instead of silence.
func TestMatrixMapEmbed(t *testing.T) {
	_, r := fixture(t, "matrix-map-embed.xml")
	page := string(r.Files["hours-location/index.html"])
	if !strings.Contains(page, `<div class="embed embed-map">`) || !strings.Contains(page, `title="Map"`) || !strings.Contains(page, "maps/embed?pb=") {
		t.Fatalf("map embed not kept: %s", page)
	}
	if strings.Contains(page, `width="600"`) || strings.Contains(page, "allowfullscreen") {
		t.Fatalf("map iframe should carry only its address, no attributes from the export: %s", page)
	}
	found := false
	for _, w := range r.Report.Warnings {
		found = found || strings.Contains(w, "Left out 1 embedded frames from other websites") && strings.Contains(w, "YouTube, Vimeo, and maps are kept")
	}
	if !found {
		t.Fatalf("missing the dropped-embeds warning: %v", r.Report.Warnings)
	}
	if !strings.Contains(themeCSS, ".embed-map{aspect-ratio:4/3}") {
		t.Fatal("theme CSS missing the map aspect-ratio rule")
	}
}

// A5: pages built with a page builder (s4 is Elementor) keep their words and
// photos in post_content but lose the builder's layout, and the importer
// used to say nothing about it. Oxygen and Bricks keep content outside
// post_content entirely, so their pages may come over empty.
func TestMatrixPageBuilders(t *testing.T) {
	c := testConverter()
	c.published = []*item{
		{Path: "/", Meta: []meta{{Key: "_elementor_edit_mode", Value: "builder"}, {Key: "_elementor_data", Value: "[{}]"}}},
		{Path: "/services/", Meta: []meta{{Key: "_elementor_edit_mode", Value: "builder"}}},
		{Path: "/about/", Meta: []meta{{Key: "ct_builder_shortcodes", Value: "1"}}},
		{Path: "/contact/", Content: "<p>Hi</p>[vc_row]Text[/vc_row]"},
	}
	c.detectBuilders()
	var elementor, oxygen, wpbakery string
	for _, w := range c.result.Report.Warnings {
		switch {
		case strings.Contains(w, "Elementor"):
			elementor = w
		case strings.Contains(w, "Oxygen"):
			oxygen = w
		case strings.Contains(w, "WPBakery"):
			wpbakery = w
		}
	}
	if elementor == "" || !strings.Contains(elementor, "2 pages were built with Elementor: /, /services/") || !strings.Contains(elementor, "simple one-column layout") {
		t.Fatalf("Elementor warning wrong: %q", elementor)
	}
	if oxygen == "" || !strings.Contains(oxygen, "1 page was built with Oxygen, which keeps page content outside the WordPress export, so these pages may be empty") {
		t.Fatalf("Oxygen warning wrong: %q", oxygen)
	}
	if wpbakery == "" || !strings.Contains(wpbakery, "1 page was built with WPBakery: /contact/") {
		t.Fatalf("WPBakery warning wrong: %q", wpbakery)
	}
}

// A6: s6's donation confirmation page is just the bare shortcode
// [give_receipt], which GiveWP recognises but WordPress's own export format
// cannot tell from plain text. A bare tag with no attributes and no closing
// tag must still be treated as a plugin shortcode when it contains an
// underscore, so it never prints as literal text on the page.
func TestMatrixUnknownPluginShortcode(t *testing.T) {
	_, r := fixture(t, "matrix-plugin-shortcode.xml")
	page := string(r.Files["donation-confirmation/index.html"])
	if strings.Contains(page, "give_receipt") {
		t.Fatalf("unregistered plugin shortcode printed as text: %s", page)
	}
}

// A7: s4's sample page links to the old site's /wp-admin/ dashboard and
// /wp-login.php, neither of which will exist once the site moves. Those
// links must be unwrapped to their own text; an ordinary link on the same
// page must be untouched.
func TestMatrixAdminLinks(t *testing.T) {
	_, r := fixture(t, "matrix-admin-links.xml")
	page := string(r.Files["sample-page/index.html"])
	if strings.Contains(page, "wp-admin") || strings.Contains(page, "wp-login") {
		t.Fatalf("admin link not unwrapped: %s", page)
	}
	if !strings.Contains(page, "your dashboard") || !strings.Contains(page, "the login page") {
		t.Fatalf("the link text should remain even though the link itself is gone: %s", page)
	}
	if !strings.Contains(page, `href="../about/"`) {
		t.Fatalf("an ordinary link on the same page must still work: %s", page)
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
	for _, name := range []string{"product-collection", "product-new", "all-products"} {
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
	for _, name := range []string{"mini-cart", "customer-account", "cart", "checkout", "breadcrumbs", "store-notices", "catalog-sorting", "product-results-count", "product-best-sellers", "product-top-rated", "handpicked-products", "product-category", "featured-product"} {
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
		{ID: "90", Type: "jetpack-portfolio", Status: "publish", Title: "Book", Link: "https://example.org/books/a-book/"},
		{ID: "91", Type: "jetpack-portfolio", Status: "publish", Password: "hidden", Title: "SECRET-BOOK", Link: "https://example.org/books/secret-book/", Content: `<img src="https://example.org/wp-content/uploads/2026/07/second.jpg">`},
		{ID: "92", Type: "tribe_events", Status: "draft", Title: "SECRET-EVENT", Link: "https://example.org/event/secret-event/", Content: `<img src="https://example.org/wp-content/uploads/2026/07/second.jpg">`},
		{ID: "93", Type: "jetpack-portfolio", Status: "publish", Title: "SECRET-PLAIN", Link: "https://example.org/?post_type=jetpack-portfolio&p=93", Content: `<img src="https://example.org/wp-content/uploads/2026/07/second.jpg">`},
		{ID: "94", Type: "jetpack-portfolio", Status: "publish", Title: "SECRET-FOREIGN", Link: "https://elsewhere.test/books/book/", Content: `<img src="https://example.org/wp-content/uploads/2026/07/second.jpg">`},
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

// Casa Lucia (s1), a restaurant with a static front page, has a "News" page
// at /news/ with its own intro paragraph and is linked from the menu; the
// posts themselves used to land at /blog/, which nothing links to. The News
// page should carry the posts list under its own intro, and /blog/ should
// not exist.
func TestMatrixPostsPageUsesLinkedPage(t *testing.T) {
	_, r := fixture(t, "matrix-postspage.xml")
	page, ok := r.Files["news/index.html"]
	if !ok {
		t.Fatal("news/index.html is missing")
	}
	h := string(page)
	if !strings.Contains(h, "Specials, events, and news from the Casa Lucia dining room.") {
		t.Fatalf("the page's own intro is missing:\n%s", h)
	}
	if !strings.Contains(h, "Fall Menu Specials Arrive This Week") || !strings.Contains(h, "Live Music Fridays Return This Fall") {
		t.Fatalf("the posts list is missing:\n%s", h)
	}
	if !strings.Contains(h, "<h1 class=\"entry-title\">News</h1>") {
		t.Fatalf("the page's own title should be the heading:\n%s", h)
	}
	if _, ok := r.Files["blog/index.html"]; ok {
		t.Error("posts were also written to /blog/, which nothing links to")
	}
}

// Trail Notes (s2) has threaded comments (wp:comment_parent): a reply must
// nest under its parent, and a reply to an unapproved comment must still
// show, at the top level, like WordPress does.
func TestMatrixThreadedComments(t *testing.T) {
	_, r := fixture(t, "matrix-comments.xml")
	page, ok := r.Files["cascade-pass/index.html"]
	if !ok {
		t.Fatal("cascade-pass/index.html is missing")
	}
	h := string(page)
	ben := strings.Index(h, "Ben Walsh")
	nora := strings.Index(h, "Nora Ellis")
	sam := strings.Index(h, "That matches our experience too.")
	children := strings.Index(h, `<div class="children">`)
	if ben < 0 || nora < 0 || sam < 0 || children < 0 || children > nora {
		t.Fatalf("reply nesting is missing or out of order:\n%s", h)
	}
	if !strings.Contains(h, "Yes, it reopened last week.") {
		t.Fatalf("a reply to an unapproved comment was dropped instead of shown at top level:\n%s", h)
	}
	// The reply's own nested <div class="children"> must come after Nora's
	// text, proving Sam is nested a level deeper than Ben, not a sibling.
	if !strings.Contains(h[nora:], `<div class="children">`) {
		t.Fatalf("second-level reply is not nested:\n%s", h)
	}
}

// Trail Notes (s2) has two authors with public posts: each post should show
// a byline linking to that author's archive, and the archive itself should
// exist for an author whose login (not a ready-made slug) needs sanitising.
func TestMatrixAuthorArchives(t *testing.T) {
	_, r := fixture(t, "matrix-authors.xml")
	post := string(r.Files["timberline-trail-report/index.html"])
	if !strings.Contains(post, `By <a href="../author/dana-cole/">Dana Cole</a>`) {
		t.Fatalf("byline with a sanitised author slug is missing:\n%s", post)
	}
	archive, ok := r.Files["author/dana-cole/index.html"]
	if !ok {
		t.Fatal("author/dana-cole/index.html is missing")
	}
	if !strings.Contains(string(archive), "Timberline Trail Report") || strings.Contains(string(archive), "Cascades in September") {
		t.Fatalf("author archive has the wrong posts:\n%s", archive)
	}
}

// Casa Verde Tours (s7) runs Polylang: pages should carry their own
// language, use their own language's menu, and the dead "#pll_switcher"
// link should become real translation links, falling back to the other
// language's front page when a page (About Us) has no translation of its
// own.
func TestMatrixPolylangLanguageSwitcher(t *testing.T) {
	_, r := fixture(t, "matrix-polylang.xml")
	home := string(r.Files["index.html"])
	es := string(r.Files["es/index.html"])
	about := string(r.Files["about-us/index.html"])
	if !strings.Contains(home, `<html lang="en">`) {
		t.Fatalf("front page should be tagged English:\n%s", home)
	}
	if !strings.Contains(es, `<html lang="es">`) {
		t.Fatalf("Spanish page should be tagged Spanish:\n%s", es)
	}
	if strings.Contains(es, "About Us") || !strings.Contains(es, "Inicio") {
		t.Fatalf("Spanish page is using the English menu:\n%s", es)
	}
	if strings.Contains(about, "#pll_switcher") {
		t.Fatalf("the dead language-switcher link was not replaced:\n%s", about)
	}
	if !strings.Contains(about, `href="../"`) || !strings.Contains(about, `href="../es/">Español`) {
		t.Fatalf("switcher should fall back to the other language's front page:\n%s", about)
	}
}

// s6's newsletter box is MailPoet's [mailpoet_form id="1"]: the report names
// the plugin, not the shortcode. s7's language menu item has no address of
// its own, so it is plain text, not a link to the page itself.
func TestMatrixPluginNamesAndEmptyMenuLinks(t *testing.T) {
	r := matrixLegBConvert(t, []*item{{ID: "1", Type: "page", Status: "publish", Title: "Home", Slug: "home", Link: "https://example.org/", Content: `[mailpoet_form id="1"]`}})
	if f := featureByKind(r, FeatureSubscribe); f == nil || f.Source != "MailPoet" {
		t.Fatalf("subscribe source: %+v", f)
	}
	c := testConverter()
	got := c.menuHTML([]*menuItem{{Title: "Languages", Children: []*menuItem{{Title: "English", URL: "/"}}}}, "/")
	if strings.Contains(got, `href=""`) || !strings.Contains(got, "<summary>Languages</summary>") {
		t.Fatalf("empty menu link: %s", got)
	}
}

// Security review of the matrix fixes: plugin post types that hold private
// data (Contact Form 7's Flamingo inbox, Easy Digital Downloads payments,
// WooCommerce's order placeholders) are published even with pretty links;
// only known public types are carried.
func TestMatrixPluginDataTypesNeverPublished(t *testing.T) {
	r := matrixLegBConvert(t, []*item{
		{ID: "1", Type: "page", Status: "publish", Title: "Home", Link: "https://example.org/"},
		{ID: "2", Type: "flamingo_inbound", Status: "publish", Title: "SECRET-INBOX", Link: "https://example.org/flamingo-inbound-1/", Content: "SECRET-INBOX jane@example.com"},
		{ID: "3", Type: "edd_payment", Status: "publish", Title: "SECRET-EDD", Link: "https://example.org/edd-payment/3/"},
		{ID: "4", Type: "shop_order_placehold", Status: "publish", Title: "SECRET-ORDER", Link: "https://example.org/shop_order_placehold/4/"},
	})
	for p, b := range r.Files {
		if strings.Contains(string(b), "SECRET-") {
			t.Errorf("%s publishes plugin data", p)
		}
	}
	found := false
	for _, w := range r.Report.Warnings {
		found = found || strings.Contains(w, "Left out 3 items that plugins stored in the export (edd_payment, flamingo_inbound, shop_order_placehold)")
	}
	if !found {
		t.Fatalf("no note about the left-out plugin items: %v", r.Report.Warnings)
	}
}

// A crafted author_nicename ("..") must not turn the author archive into
// the home page, and a 20,000-reply chain must render in linear time.
func TestMatrixAuthorSlugAndLongCommentChains(t *testing.T) {
	doc := `<rss xmlns:wp="http://wordpress.org/export/1.2/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><title>Site</title><link>https://example.org</link>` +
		`<wp:author><wp:author_login>evil</wp:author_login><wp:author_nicename>..</wp:author_nicename><wp:author_display_name>Evil</wp:author_display_name></wp:author>` +
		`<wp:author><wp:author_login>ann</wp:author_login><wp:author_nicename>ann</wp:author_nicename><wp:author_display_name>Ann</wp:author_display_name></wp:author>` +
		`<item><title>Real Homepage</title><link>https://example.org/2026/01/real/</link><dc:creator>ann</dc:creator><wp:post_id>1</wp:post_id><wp:post_type>post</wp:post_type><wp:status>publish</wp:status><wp:post_date>2026-01-02 00:00:00</wp:post_date><content:encoded>Hello</content:encoded></item>` +
		`<item><title>By evil</title><link>https://example.org/2026/01/evil/</link><dc:creator>evil</dc:creator><wp:post_id>2</wp:post_id><wp:post_type>post</wp:post_type><wp:status>publish</wp:status><wp:post_date>2026-01-01 00:00:00</wp:post_date><wp:comment_status>open</wp:comment_status><content:encoded>Hi</content:encoded>`
	var b strings.Builder
	b.WriteString(doc)
	for n := 1; n <= 20000; n++ {
		fmt.Fprintf(&b, `<wp:comment><wp:comment_id>%d</wp:comment_id><wp:comment_author>a</wp:comment_author><wp:comment_content>c</wp:comment_content><wp:comment_approved>1</wp:comment_approved><wp:comment_parent>%d</wp:comment_parent></wp:comment>`, n, n-1)
	}
	b.WriteString(`</item></channel></rss>`)
	start := time.Now()
	r, err := Convert(context.Background(), Input{WXR: []io.Reader{strings.NewReader(b.String())}})
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Fatalf("20,000 chained comments took %s", d)
	}
	if home := string(r.Files["index.html"]); strings.Contains(home, "Posts by Evil") || !strings.Contains(home, "Real Homepage") {
		t.Fatalf("author archive overwrote the home page")
	}
	if _, ok := r.Files["author/evil/index.html"]; !ok {
		t.Fatalf("the crafted author's archive should land under /author/: %v", sortedKeys(r.Files)[:10])
	}
}

func TestMatrixDuplicateCommentIDsAndBoundedTraversal(t *testing.T) {
	if os.Getenv("WPMATRIX_COMMENT_CHILD") == "1" {
		debug.SetMaxStack(128 << 10)
		cs := []comment{
			{ID: "1", Parent: "2", Content: "first-one"},
			{ID: "2", Parent: "1", Content: "first-two"},
			{ID: "1", Parent: "0", Content: "duplicate-one"},
			{ID: "2", Parent: "0", Content: "duplicate-two"},
		}
		h := renderCommentNodes(buildCommentTree(cs), 1)
		if strings.Count(h, `class="comment"`) != 2 || strings.Contains(h, "duplicate-") {
			t.Fatal("duplicate IDs were attached more than once", h)
		}
		cs = nil
		for n := 1; n <= 20000; n++ {
			cs = append(cs, comment{ID: fmt.Sprint(n), Parent: fmt.Sprint(n - 1), Content: "reply"})
		}
		h = renderCommentNodes(buildCommentTree(cs), 1)
		if strings.Count(h, `class="comment"`) != len(cs) {
			t.Fatal("long chain lost replies")
		}
		return
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-test.run=^TestMatrixDuplicateCommentIDsAndBoundedTraversal$")
	cmd.Env = append(os.Environ(), "WPMATRIX_COMMENT_CHILD=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("comment traversal failed: %v\n%s", err, out)
	}
}

func TestMatrixAdminLinkChildrenStillCleaned(t *testing.T) {
	r := matrixLegBConvert(t, []*item{{ID: "1", Type: "page", Status: "publish", Title: "Home", Link: "https://example.org/", Content: `<a href="/wp-admin/"><span onclick="alert(1)"><img src="https://example.org/wp-content/uploads/2026/07/photo.png" onerror="alert(2)"><script>alert(3)</script></span><iframe src="https://elsewhere.test/private"></iframe><form action="https://example.org/send"><label>Email<input type="email" name="email"></label></form></a>`}})
	h := string(r.Files["index.html"])
	for _, bad := range []string{"onerror", "onclick", "<script>alert(", "elsewhere.test/private", "wp-admin", "example.org/wp-content/uploads", "example.org/send"} {
		if strings.Contains(h, bad) {
			t.Error("admin-link child escaped cleaning:", bad, h)
		}
	}
	if !strings.Contains(h, `src="wp-content/uploads/2026/07/photo.png"`) || !strings.Contains(h, `data-sh-feature="contact"`) {
		t.Fatal("moved children were not rewritten or converted", h)
	}
}

func TestMatrixGalleryAndThumbnailAttachmentVisibility(t *testing.T) {
	for _, tc := range []struct {
		kind, status, password string
		visible                bool
	}{
		{"attachment", "inherit", "", true}, {"attachment", "publish", "", true},
		{"attachment", "private", "", false}, {"attachment", "draft", "", false},
		{"attachment", "future", "", false}, {"attachment", "pending", "", false},
		{"attachment", "inherit", "secret", false}, {"attachment", "publish", "secret", false},
		{"page", "private", "", false},
	} {
		t.Run(tc.kind+"/"+tc.status+"/"+tc.password, func(t *testing.T) {
			r := matrixLegBConvert(t, []*item{
				{ID: "1", Type: "product", Status: "publish", Title: "Product", Link: "https://example.org/product/", Content: `[gallery ids="2"]`, Meta: []meta{{"_product_image_gallery", "2"}, {"_thumbnail_id", "2"}}},
				{ID: "2", Type: tc.kind, Status: tc.status, Password: tc.password, Title: "ATTACHMENT-TITLE", Excerpt: "ATTACHMENT-CAPTION", Attachment: "https://example.org/wp-content/uploads/2026/07/photo.png"},
			})
			h := string(r.Files["product/index.html"])
			if strings.Contains(h, "ATTACHMENT-TITLE") != tc.visible || strings.Contains(h, "ATTACHMENT-CAPTION") != tc.visible {
				t.Fatal("wrong attachment visibility", h)
			}
			_, copied := r.Files["wp-content/uploads/2026/07/photo.png"]
			if copied != tc.visible {
				t.Fatal("wrong attachment copying", copied)
			}
		})
	}
}

func TestMatrixPostsIndexAvoidsEveryPublicItem(t *testing.T) {
	r := matrixLegBConvert(t, []*item{
		{ID: "1", Type: "page", Status: "publish", Title: "Home", Slug: "home", Link: "https://example.org/home/"},
		{ID: "2", Type: "post", Status: "publish", Title: "Real post", Slug: "blog", Link: "https://example.org/blog/", Content: "FULL ORIGINAL BODY", Excerpt: "SHORT EXCERPT"},
		{ID: "3", Type: "product", Status: "publish", Title: "Real product", Link: "https://example.org/blog/posts/", Content: "FULL PRODUCT BODY"},
	})
	if !strings.Contains(string(r.Files["blog/index.html"]), "FULL ORIGINAL BODY") || !strings.Contains(string(r.Files["blog/posts/index.html"]), "FULL PRODUCT BODY") {
		t.Fatal("posts index overwrote a public item")
	}
	if !strings.Contains(string(r.Files["blog/posts/posts/index.html"]), "SHORT EXCERPT") {
		t.Fatal("posts index did not move past both occupied paths")
	}
}

func TestMatrixPostsPageKeepsCompleteBody(t *testing.T) {
	for _, withPost := range []bool{false, true} {
		t.Run(fmt.Sprint(withPost), func(t *testing.T) {
			items := []*item{
				{ID: "1", Type: "page", Status: "publish", Title: "Home", Link: "https://example.org/"},
				{ID: "2", Type: "page", Status: "publish", Slug: "news", Title: "News", Link: "https://example.org/news/", Content: "News intro", Meta: []meta{{"_thumbnail_id", "3"}}, Comments: []comment{{ID: "1", Approved: "1", Content: "APPROVED-READER-COMMENT"}}},
				{ID: "3", Type: "attachment", Status: "inherit", Title: "News photo", Attachment: "https://example.org/wp-content/uploads/2026/07/photo.png"},
			}
			if withPost {
				items = append(items, &item{ID: "4", Type: "post", Status: "publish", Title: "Listed post", Link: "https://example.org/listed/"})
			}
			r := matrixLegBConvert(t, items)
			h := string(r.Files["news/index.html"])
			for _, want := range []string{"News intro", "APPROVED-READER-COMMENT", `class="featured-image"`, `src="../wp-content/uploads/2026/07/photo.png"`} {
				if !strings.Contains(h, want) {
					t.Error("posts page lost", want)
				}
			}
			if r.Report.Comments != 1 || strings.Count(h, "APPROVED-READER-COMMENT") != 1 {
				t.Fatal("comments rendered or counted twice")
			}
			if withPost && strings.Index(h, "APPROVED-READER-COMMENT") > strings.Index(h, ">Listed post</a>") {
				t.Fatal("posts list should follow the full page body")
			}
		})
	}
}

func TestMatrixMissingAuthorRecordsUsePostLogins(t *testing.T) {
	r := matrixLegBConvert(t, []*item{
		{ID: "1", Type: "post", Status: "publish", Author: "alice", Title: "Alice post", Link: "https://example.org/alice-post/"},
		{ID: "2", Type: "post", Status: "publish", Author: "bob", Title: "Bob post", Link: "https://example.org/bob-post/"},
	})
	for _, login := range []string{"alice", "bob"} {
		h := string(r.Files["author/"+login+"/index.html"])
		if !strings.Contains(h, "Posts by "+login) || !strings.Contains(string(r.Files[login+"-post/index.html"]), `href="../author/`+login+`/"`) {
			t.Fatal("missing author record did not use post login", login)
		}
	}
	if strings.Contains(string(r.Files["author/alice/index.html"]), "Bob post") || strings.Contains(string(r.Files["author/bob/index.html"]), "Alice post") {
		t.Fatal("unrelated authors share an archive")
	}
}

func matrixMenu(id, title, group, kind, target string) *item {
	key := "_menu_item_object_id"
	if kind == "custom" {
		key = "_menu_item_url"
	}
	return &item{ID: id, Type: "nav_menu_item", Status: "publish", Title: title, Categories: []category{{Domain: "nav_menu", Slug: group}}, Meta: []meta{{"_menu_item_type", kind}, {key, target}}}
}

func TestMatrixGeneratedArchiveLinksRewritten(t *testing.T) {
	paths := []string{"author/alice/", "events/", "shop/", "product-category/cups/"}
	var body strings.Builder
	for _, p := range paths {
		body.WriteString(`<a href="https://example.org/` + p + `">Archive</a>`)
	}
	r := matrixLegBConvert(t, []*item{
		{ID: "1", Type: "page", Status: "publish", Title: "Home", Link: "https://example.org/", Content: body.String()},
		{ID: "2", Type: "post", Status: "publish", Author: "alice", Title: "Post", Link: "https://example.org/post/"},
		{ID: "3", Type: "tribe_events", Status: "publish", Title: "Event", Link: "https://example.org/event/"},
		{ID: "4", Type: "page", Status: "publish", Title: "Shop", Slug: "shop", Link: "https://example.org/shop/"},
		{ID: "5", Type: "product", Status: "publish", Title: "Cup", Link: "https://example.org/cup/", Categories: []category{{Domain: "product_cat", Slug: "cups", Name: "Cups"}}},
		matrixMenu("6", "Alice's posts", "main", "custom", "https://example.org/author/alice/"),
	})
	h := string(r.Files["index.html"])
	for _, p := range paths {
		if _, ok := r.Files[p+"index.html"]; !ok || !strings.Contains(h, `href="`+p+`"`) || strings.Contains(h, `href="https://example.org/`+p+`"`) {
			t.Error("archive path not recognised", p)
		}
	}
}

func TestMatrixPluginFormsKeepTheirOwnIdentifiers(t *testing.T) {
	r := matrixLegBConvert(t, []*item{
		{ID: "1", Type: "page", Status: "publish", Title: "Contact", Link: "https://example.org/contact/", Content: `[contact-form-7 id="2"] [contact-form-7 id="3"]`},
		{ID: "2", Type: "wpcf7_contact_form", Status: "publish", Title: "Sales", Meta: []meta{{"_form", `[email* email] [textarea message]`}}},
		{ID: "3", Type: "wpcf7_contact_form", Status: "publish", Title: "Support", Meta: []meta{{"_form", `[email* email] [textarea message]`}}},
	})
	h := string(r.Files["contact/index.html"])
	if !strings.Contains(h, `data-form="Sales"`) || !strings.Contains(h, `data-form="Support"`) || strings.Contains(h, `data-form="Contact Form 7"`) {
		t.Fatal("individual form identity lost", h)
	}
	if f := featureByKind(r, FeatureContactForm); f == nil || f.Source != "Contact Form 7" {
		t.Fatal("report should still name the plugin", f)
	}
}

func TestMatrixWooCommerceBlockSelections(t *testing.T) {
	for _, tc := range []struct {
		name, attrs string
		want        []string
		dropped     bool
	}{
		{"featured-product", `{"productId":3}`, []string{"Selected"}, false},
		{"featured-product", `{"productId":99}`, nil, true},
		{"handpicked-products", `{"products":[4,3]}`, []string{"Sale", "Selected"}, false},
		{"handpicked-products", `{"products":[3,99]}`, nil, true},
		{"product-category", `{"categories":[12]}`, []string{"Selected"}, false},
		{"product-category", `{"categories":["cups"]}`, []string{"Selected"}, false},
		{"product-category", `{"categories":["missing"]}`, nil, true},
		{"product-category", `{"categories":[12,"missing"]}`, nil, true},
		{"product-on-sale", `{}`, []string{"Sale"}, false},
		{"product-collection", `{"query":{"woocommerceOnSale":true}}`, []string{"Sale"}, false},
		{"all-products", `{"orderBy":"title","order":"asc","offset":1,"perPage":1}`, []string{"Selected"}, false},
		{"all-products", `{"orderBy":"rating"}`, nil, true},
		{"product-collection", `{"collection":"woocommerce/product-collection/cross-sells"}`, nil, true},
		{"product-collection", `{"collection":"woocommerce/product-collection/handpicked"}`, nil, true},
		{"product-collection", `{"query":{"woocommerceHandPickedProducts":"3"}}`, nil, true},
	} {
		t.Run(tc.name+tc.attrs, func(t *testing.T) {
			c := testConverter()
			c.published = []*item{
				{ID: "2", Type: "product", Status: "publish", Title: "Unrelated", Path: "/unrelated/", Meta: []meta{{"_price", "100"}, {"_regular_price", "100"}, {"_sale_price", "50"}}},
				{ID: "3", Type: "product", Status: "publish", Title: "Selected", Path: "/selected/", Categories: []category{{Domain: "product_cat", Slug: "cups"}}},
				{ID: "4", Type: "product", Status: "publish", Title: "Sale", Path: "/sale/", Meta: []meta{{"_price", "50"}, {"_regular_price", "100"}, {"_sale_price", "50"}}},
				{ID: "99", Type: "product", Status: "private", Title: "Private", Path: "/private/"},
			}
			c.terms["product_cat:cups"] = term{ID: "12", Taxonomy: "product_cat", Slug: "cups"}
			h := c.renderContent(`<!-- wp:woocommerce/`+tc.name+` `+tc.attrs+` /-->`, "/", 0)
			if strings.Count(h, `class="product-list-item"`) != len(tc.want) {
				t.Fatal("wrong selection", h)
			}
			last := -1
			for _, title := range tc.want {
				at := strings.Index(h, ">"+title+"</a>")
				if at <= last {
					t.Fatal("selection or order lost", h)
				}
				last = at
			}
			if (c.result.Report.Dropped["block:woocommerce/"+tc.name] > 0) != tc.dropped {
				t.Fatal("unusable selection not counted", c.result.Report.Dropped)
			}
		})
	}
}

func TestMatrixSalePriceMustBeCurrent(t *testing.T) {
	for _, tc := range []struct {
		current, from, to string
		active            bool
	}{
		{"100", "4102444800", "", false}, {"100", "", "1", false}, {"50", "", "", true},
	} {
		t.Run(tc.current+tc.from+tc.to, func(t *testing.T) {
			r := matrixLegBConvert(t, []*item{
				{ID: "1", Type: "page", Status: "publish", Title: "Shop", Slug: "shop", Link: "https://example.org/shop/"},
				{ID: "2", Type: "product", Status: "publish", Title: "Cup", Link: "https://example.org/cup/", Meta: []meta{{"_price", tc.current}, {"_regular_price", "100"}, {"_sale_price", "50"}, {"_sale_price_dates_from", tc.from}, {"_sale_price_dates_to", tc.to}}},
			})
			for _, p := range []string{"cup/index.html", "shop/index.html"} {
				h := string(r.Files[p])
				if strings.Contains(h, "Was 100") != tc.active || !strings.Contains(h, `class="product-price">`+tc.current) {
					t.Error("price is not current", p, h)
				}
			}
		})
	}
}

func TestMatrixBareUnderscoreLiteralsAndKnownPlugins(t *testing.T) {
	var content strings.Builder
	content.WriteString("Use [user_id] as a literal field name. ")
	for _, prefix := range []string{"give_", "woocommerce_", "edd_", "wpforms_", "et_pb_", "vc_", "mailpoet_", "jetpack_", "tribe_", "wpcf7", "gravityform", "ninja_", "formidable"} {
		content.WriteString("[" + prefix + "unused] ")
	}
	r := matrixLegBConvert(t, []*item{{ID: "1", Type: "page", Status: "publish", Title: "Home", Link: "https://example.org/", Content: content.String()}})
	for _, p := range []string{"index.html", "search-index.json"} {
		h := string(r.Files[p])
		if !strings.Contains(h, "[user_id]") || strings.Contains(h, "unused]") {
			t.Error("literal or plugin classification wrong", p, h)
		}
	}
}

func TestMatrixLanguageMenuUsesDefaultScoring(t *testing.T) {
	for _, preferred := range []bool{true, false} {
		t.Run(fmt.Sprint(preferred), func(t *testing.T) {
			c := testConverter()
			c.byID["1"] = &item{ID: "1", Type: "page", Status: "publish", Path: "/en/", Categories: []category{{Domain: "language", Slug: "en"}}}
			group := "a-navigation-en"
			if preferred {
				group = "main-en"
			}
			for n := 0; n < 4; n++ {
				i := matrixMenu(fmt.Sprint(10+n), "PRIMARY", group, "post_type", "1")
				if n > 0 {
					i.Meta = append(i.Meta, meta{"_menu_item_menu_item_parent", "10"})
				}
				c.data.Items = append(c.data.Items, i)
			}
			footerSize := 2
			if preferred {
				footerSize = 6
			}
			for n := 0; n < footerSize; n++ {
				c.data.Items = append(c.data.Items, matrixMenu(fmt.Sprint(20+n), "FOOTER", "z-footer-en", "post_type", "1"))
			}
			c.headerMenu = c.buildMenu()
			if c.headerMenu[0].Title != "PRIMARY" || c.menuFor("en")[0].Title != "PRIMARY" {
				t.Fatal("language menu did not share default preference and total-count scoring")
			}
		})
	}
}

func TestMatrixLocalizedPostsPageAndPagination(t *testing.T) {
	items := []*item{
		{ID: "1", Type: "page", Status: "publish", Title: "Home", Link: "https://example.org/", Categories: []category{{Domain: "language", Slug: "en"}}},
		{ID: "2", Type: "page", Status: "publish", Title: "Inicio", Link: "https://example.org/es/", Categories: []category{{Domain: "language", Slug: "es"}}},
		{ID: "3", Type: "page", Status: "publish", Title: "Noticias", Slug: "news", Link: "https://example.org/news/", Content: "Spanish intro", Categories: []category{{Domain: "language", Slug: "es"}, {Domain: "post_translations", Slug: "news-translations"}}},
		{ID: "4", Type: "page", Status: "publish", Title: "Guide", Slug: "guide", Link: "https://example.org/guide/", Categories: []category{{Domain: "language", Slug: "en"}, {Domain: "post_translations", Slug: "news-translations"}}},
		{ID: "5", Type: "post", Status: "publish", Title: "ENGLISH-POST", Link: "https://example.org/english-post/", Categories: []category{{Domain: "language", Slug: "en"}}},
		matrixMenu("10", "ENGLISH-MENU", "main-en", "post_type", "1"),
		matrixMenu("11", "English guide", "main-en", "post_type", "4"),
		matrixMenu("12", "English extra", "main-en", "custom", "/search/"),
		matrixMenu("13", "SPANISH-MENU", "main-es", "post_type", "2"),
		matrixMenu("14", "Spanish news", "main-es", "post_type", "3"),
		matrixMenu("15", "Languages", "main-es", "custom", "#pll_switcher"),
	}
	for n := 0; n < 21; n++ {
		items = append(items, &item{ID: fmt.Sprint(100 + n), Type: "post", Status: "publish", Title: fmt.Sprintf("SPANISH-POST-%d", n), Link: fmt.Sprintf("https://example.org/es/post-%d/", n), Categories: []category{{Domain: "language", Slug: "es"}}})
	}
	r := matrixLegBConvert(t, items, term{Taxonomy: "post_translations", Slug: "news-translations", Description: `a:2:{s:2:"en";i:4;s:2:"es";i:3;}`})
	for _, p := range []string{"news/index.html", "news/page/2/index.html"} {
		h := string(r.Files[p])
		if !strings.Contains(h, `<html lang="es">`) || !strings.Contains(h, "SPANISH-MENU") || !strings.Contains(h, "SPANISH-POST-") || strings.Contains(h, "ENGLISH-MENU") || strings.Contains(h, "ENGLISH-POST") {
			t.Error("localized archive lost language, menu, or selection", p, h)
		}
		if !strings.Contains(h, `guide/">English`) || strings.Contains(h, "#pll_switcher") {
			t.Error("posts-page translations lost", p, h)
		}
	}
	if !strings.Contains(string(r.Files["news/index.html"]), "Spanish intro") {
		t.Fatal("posts page lost its content")
	}
}

func TestMatrixSynthesizedPagesUseTheirLanguageMenu(t *testing.T) {
	r := matrixLegBConvert(t, []*item{
		{ID: "1", Type: "page", Status: "publish", Title: "Inicio", Link: "https://example.org/", Categories: []category{{Domain: "language", Slug: "es"}}},
		{ID: "2", Type: "page", Status: "publish", Title: "Home", Link: "https://example.org/en/", Categories: []category{{Domain: "language", Slug: "en"}}},
		{ID: "3", Type: "tribe_events", Status: "publish", Title: "Event", Link: "https://example.org/event/", Categories: []category{{Domain: "language", Slug: "es"}}},
		{ID: "4", Type: "post", Status: "publish", Author: "alice", Date: "2026-10-15 00:00:00", Title: "Post", Link: "https://example.org/post/", Categories: []category{{Domain: "language", Slug: "es"}, {Domain: "category", Slug: "notes", Name: "Notes"}}},
		matrixMenu("10", "SPANISH-MENU", "main-es", "post_type", "1"),
		matrixMenu("11", "Languages", "main-es", "custom", "#pll_switcher"),
		matrixMenu("12", "ENGLISH-MENU", "primary-en", "post_type", "2"),
		matrixMenu("13", "Languages", "primary-en", "custom", "#pll_switcher"),
		matrixMenu("14", "English extra", "primary-en", "custom", "/search/"),
		{ID: "5", Type: "post", Status: "publish", Author: "alice", Date: "2000-01-01 00:00:00", Title: "ENGLISH-ARCHIVE-POST", Link: "https://example.org/en/post/", Categories: []category{{Domain: "language", Slug: "en"}, {Domain: "category", Slug: "notes", Name: "Notes"}}},
		{ID: "6", Type: "post", Status: "publish", Title: "English category post", Link: "https://example.org/en/category-post/", Categories: []category{{Domain: "language", Slug: "en"}, {Domain: "category", Slug: "english-notes", Name: "English notes"}}},
	})
	english := string(r.Files["category/english-notes/index.html"])
	if !strings.Contains(english, `<html lang="en">`) || !strings.Contains(english, "ENGLISH-MENU") || !strings.Contains(english, "English category post") {
		t.Fatal("category archive did not inherit its posts' language", english)
	}
	for _, p := range []string{"events/index.html", "blog/index.html", "category/notes/index.html", "search/index.html", "404.html", "author/alice/index.html"} {
		h := string(r.Files[p])
		if !strings.Contains(h, `<html lang="es">`) || !strings.Contains(h, "SPANISH-MENU") || strings.Contains(h, "ENGLISH-MENU") || strings.Contains(h, "#pll_switcher") {
			t.Error("synthesized page did not resolve its language menu", p, h)
		}
		if p != "search/index.html" && strings.Contains(h, "ENGLISH-ARCHIVE-POST") {
			t.Error("archive mixed languages", p)
		}
	}
}

func TestMatrixJPEGFillBytesBeforeOrientation(t *testing.T) {
	jpg := insertExifOrientation(encodeTestJPEG(t, 40, 20), 6)
	for _, count := range []int{1, 4} {
		filled := append(append(append([]byte(nil), jpg[:2]...), bytes.Repeat([]byte{0xff}, count)...), jpg[2:]...)
		out, note := shrinkImage(filled, DefaultLimits)
		cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
		if err != nil || note != "" || cfg.Width != 20 || cfg.Height != 40 {
			t.Fatal("marker fill bytes prevented rotation", count, cfg, note, err)
		}
	}
	if jpegOrientation([]byte{0xff, 0xd8, 0xff, 0xff}) != 0 {
		t.Fatal("truncated marker fill should return unknown orientation")
	}
}

func TestMatrixVariationsReusedAcrossPriceAndOptions(t *testing.T) {
	c := testConverter()
	product := &item{ID: "1", Type: "product", Status: "publish"}
	for n, status := range []string{"publish", "private", "publish"} {
		id := fmt.Sprint(n + 2)
		v := &item{ID: id, Type: "product_variation", Parent: "1", Status: status, Meta: []meta{{"_price", fmt.Sprint(10 + n)}, {"attribute_size", "small"}}}
		if n == 2 {
			v.Password = "secret"
		}
		c.data.Items = append(c.data.Items, v)
		c.byID[id] = v
	}
	if c.productPrice(product) != "10" || !strings.Contains(c.productOptions(product), "Size: Small") || len(c.variations(product)) != 1 {
		t.Fatal("variation visibility or rendering changed")
	}
	if allocations := testing.AllocsPerRun(100, func() { c.variations(product) }); allocations != 0 {
		t.Fatalf("repeated variation lookup still allocates a new list: %g", allocations)
	}
}

func TestMatrixAuthorArchivesGroupPostsOnce(t *testing.T) {
	c := testConverter()
	// One post per author is the largest sparse grouping allowed by the importer.
	for n := 0; n < 20000; n++ {
		login := fmt.Sprintf("author-%d", n)
		c.data.Authors = append(c.data.Authors, author{Login: login, Nicename: login})
		c.posts = append(c.posts, &item{ID: fmt.Sprint(n), Type: "post", Status: "publish", Author: login, Title: login, Path: fmt.Sprintf("/post-%d/", n)})
	}
	authors, _ := c.authorInfo()
	start := time.Now()
	c.authorArchives(authors, nil)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("20,000 sparse author archives rescanned all posts: %s", elapsed)
	}
	for _, n := range []int{0, 19999} {
		login := fmt.Sprintf("author-%d", n)
		h := string(c.result.Files["author/"+login+"/index.html"])
		if strings.Count(h, `<article><h2>`) != 1 || !strings.Contains(h, ">"+login+"</a>") {
			t.Fatal("author grouping lost or mixed posts", login)
		}
	}
}

func TestMatrixEventsExistenceDoesNotResort(t *testing.T) {
	c := testConverter()
	for n := 0; n < 2000; n++ {
		c.published = append(c.published, &item{ID: fmt.Sprint(n), Type: "tribe_events", Status: "publish", Path: fmt.Sprintf("/event-%d/", n), Meta: []meta{{"_EventStartDate", fmt.Sprint((n * 7919) % 2000)}}})
	}
	if !c.pageExists("/events/") || c.rewriteURL("https://example.org/events/", "/event-1/") != "../events/" {
		t.Fatal("synthetic events index not recognised")
	}
	if allocations := testing.AllocsPerRun(100, func() { c.pageExists("/events/") }); allocations != 0 {
		t.Fatalf("event existence checks still allocate and sort: %g", allocations)
	}
	if events := c.events(); len(events) != 2000 || events[0].metadata("_EventStartDate") != "0" {
		t.Fatal("cached event list is not sorted")
	}
}

func TestMatrixNonpublicClassicMenuItemsExcluded(t *testing.T) {
	for _, tc := range []struct{ status, password string }{{"draft", ""}, {"future", ""}, {"pending", ""}, {"private", ""}, {"publish", "secret"}} {
		t.Run(tc.status+tc.password, func(t *testing.T) {
			i := matrixMenu("2", "SECRET-MENU", "main", "custom", "https://elsewhere.test/secret/")
			i.Status, i.Password = tc.status, tc.password
			r := matrixLegBConvert(t, []*item{{ID: "1", Type: "page", Status: "publish", Title: "Home", Link: "https://example.org/"}, i, matrixMenu("3", "Public menu", "main", "custom", "https://elsewhere.test/public/")})
			for p, b := range r.Files {
				if strings.Contains(string(b), "SECRET-MENU") || strings.Contains(string(b), "elsewhere.test/secret/") {
					t.Error("nonpublic menu emitted", p)
				}
			}
			if !strings.Contains(string(r.Files["index.html"]), "Public menu") {
				t.Fatal("public menu lost")
			}
		})
	}
}

func TestMatrixFormLookupRequiresPublicExpectedType(t *testing.T) {
	for _, plugin := range []struct{ tag, kind, id string }{{"wpforms", "wpforms", "2"}, {"contact-form-7", "wpcf7_contact_form", "2"}, {"contact-form-7", "wpcf7_contact_form", "form-hash"}} {
		for _, tc := range []struct{ kind, status, password string }{{"page", "private", ""}, {"page", "publish", ""}, {plugin.kind, "private", ""}, {plugin.kind, "draft", ""}, {plugin.kind, "publish", "secret"}} {
			t.Run(plugin.tag+plugin.id+tc.kind+tc.status+tc.password, func(t *testing.T) {
				r := matrixLegBConvert(t, []*item{
					{ID: "1", Type: "page", Status: "publish", Title: "Contact", Link: "https://example.org/contact/", Content: `[` + plugin.tag + ` id="` + plugin.id + `"]`},
					{ID: "2", Type: tc.kind, Status: tc.status, Password: tc.password, Title: "OTHER-ITEM-TITLE", Link: "https://example.org/other/", Meta: []meta{{"_hash", "form-hash"}}},
				})
				h := string(r.Files["contact/index.html"])
				// An unrelated public page can appear in the fallback menu, but not as a form heading.
				if strings.Contains(h, "<h2>OTHER-ITEM-TITLE</h2>") || strings.Contains(h, `data-form="OTHER-ITEM-TITLE"`) || !strings.Contains(h, `data-sh-feature="contact"`) {
					t.Fatal("form lookup exposed another item or lost fallback form", h)
				}
			})
		}
	}
}

func TestMatrixTermArchivesRequirePublicUse(t *testing.T) {
	r := matrixLegBConvert(t, []*item{
		{ID: "1", Type: "page", Status: "publish", Title: "Home", Link: "https://example.org/", Content: `<!-- wp:categories /--><!-- wp:tag-cloud /-->`},
		{ID: "2", Type: "post", Status: "private", Categories: []category{{Domain: "category", Slug: "SECRET-SLUG", Name: "SECRET-TITLE"}, {Domain: "post_tag", Slug: "SECRET-TAG", Name: "SECRET-TAG-TITLE"}}},
		{ID: "3", Type: "post", Status: "publish", Title: "Post", Link: "https://example.org/post/", Categories: []category{{Domain: "category", Slug: "used", Name: "Used"}}},
		matrixMenu("4", "SECRET-MENU-TERM", "main", "taxonomy", "12"),
	}, term{ID: "12", Taxonomy: "category", Slug: "SECRET-DECLARED", Name: "SECRET-DECLARED-TITLE"}, term{Taxonomy: "product_cat", Slug: "SECRET-PRODUCT", Name: "SECRET-PRODUCT-TITLE"})
	for p, b := range r.Files {
		if strings.Contains(p, "SECRET-") || strings.Contains(string(b), "SECRET-") {
			t.Error("unused or private-only taxonomy published", p)
		}
	}
	if _, ok := r.Files["category/used/index.html"]; !ok || r.Report.Categories != 1 || r.Report.Tags != 0 {
		t.Fatal("public term archive lost or unused terms counted", r.Report)
	}
}
