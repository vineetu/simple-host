package wpimport

import (
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
