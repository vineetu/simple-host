package wpimport

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
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
