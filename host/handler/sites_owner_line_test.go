package handler

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The owner line names who runs the hosted services, so it shows on
// simple-host.app and simple-hack.app only, never on someone else's install.
func TestOwnerLineOnlyOnHostedServices(t *testing.T) {
	prevHosted := instanceIsHostedService
	t.Cleanup(func() { instanceIsHostedService = prevHosted })
	cases := []struct {
		name                  string
		hosted, product       bool
		productBase, wantLine string
	}{
		{"simple-host.app", true, false, "https://product.example", "Simple Host is made and run by Vineet Sriram."},
		{"product.example", false, true, "https://product.example", "Test product is made and run by Vineet Sriram."},
		{"self-hosted host", false, false, "https://product.example", ""},
		{"self-hosted product", false, true, "https://self.product.example", ""},
	}
	for _, c := range cases {
		instanceIsHostedService = c.hosted
		kind := platformTestEdition()
		if c.product {
			kind = groupTestEdition()
		}
		assets := testAssetPipeline(kind, c.productBase)
		for _, name := range []string{"privacy.html", "support.html", "index.html"} {
			src, err := staticFiles.ReadFile("static/" + name)
			if err != nil {
				t.Fatal(err)
			}
			page, err := withChrome(src, assets.chromeDataFor(httptest.NewRequest("GET", "/", nil), ""))
			if err != nil {
				t.Fatal(err)
			}
			body := string(page)
			if strings.Contains(body, markerOwner) {
				t.Errorf("%s %s: owner marker left in the page", c.name, name)
			}
			want := 1
			if name != "index.html" {
				want = 2 // footer and the line near the top
			}
			if c.wantLine == "" {
				if strings.Contains(body, "made and run by") {
					t.Errorf("%s %s: owner line shown on an install someone else runs", c.name, name)
				}
			} else if n := strings.Count(body, c.wantLine); n != want {
				t.Errorf("%s %s: %q appears %d times, want %d", c.name, name, c.wantLine, n, want)
			}
		}
	}
}
