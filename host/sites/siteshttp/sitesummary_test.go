package siteshttp

import (
	"strings"
	"testing"
	"unicode/utf8"

	model "github.com/vsriram/simple-host/host/model"
)

func TestExtractSiteSummary(t *testing.T) {
	for _, tt := range []struct {
		name, page string
		want       model.SiteSummary
	}{
		{"normal", `<html><head><title>  Real   estate &amp; homes </title><META NAME="DESCRIPTION" CONTENT=" Find  your home "></head><body><h1>Welcome <em>home</em></h1><h2> For sale </h2><h3>Today</h3><p>See <b>our</b> listings.</p></body></html>`, model.SiteSummary{Title: "Real estate & homes", Description: "Find your home", Headings: "Welcome home | For sale | Today", Text: "Welcome home For sale Today See our listings."}},
		{"hidden content", `<head><title>Page</title><style>head css</style></head><body>Before<script>secret code</script><style>body css</style><noscript>hidden fallback</noscript><!-- secret comment --><p>After</p></body>`, model.SiteSummary{Title: "Page", Text: "Before After"}},
		// <title> is a raw-text element (like <textarea>): "<script>" inside it
		// is literal title text, never a parsed child element, so only the
		// heading case below exercises textContent's script/style exclusion.
		{"hidden content in a heading", `<h1>Welcome<script>secret_js()</script><style>.secret_css{}</style><noscript>fallback</noscript></h1>`, model.SiteSummary{Headings: "Welcome", Text: "Welcome"}},
		{"empty", ``, model.SiteSummary{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractSiteSummary(map[string][]byte{"index.html": []byte(tt.page)})
			if got != tt.want {
				t.Errorf("got %#v, want %#v", got, tt.want)
			}
		})
	}
	if got := ExtractSiteSummary(map[string][]byte{"other.html": []byte("<title>Ignore</title>")}); got != (model.SiteSummary{}) {
		t.Errorf("missing index: %#v", got)
	}
}

func TestExtractSiteSummaryByteCaps(t *testing.T) {
	got := ExtractSiteSummary(map[string][]byte{"index.html": []byte("<title>" + strings.Repeat("界", 100) + "</title><p>" + strings.Repeat("界", 150))})
	if len(got.Title) != 198 || len(got.Text) != 300 || !utf8.ValidString(got.Title+got.Text) {
		t.Fatalf("multi-byte content: %#v", got)
	}
}
