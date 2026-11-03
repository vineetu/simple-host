package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/edition"
)

// The owner line names who runs the hosted services, so it shows on
// simple-host.app and simple-hack.app only, never on someone else's install.
func TestOwnerLineOnlyOnHostedServices(t *testing.T) {
	prevHosted := instanceIsHostedService
	t.Cleanup(func() { instanceIsHostedService = prevHosted })
	cases := []struct {
		name               string
		hosted, hack       bool
		hackBase, wantLine string
	}{
		{"simple-host.app", true, false, "https://simple-hack.app", "Simple Host is made and run by Vineet Sriram."},
		{"simple-hack.app", false, true, "https://simple-hack.app", "Simple Hack is made and run by Vineet Sriram."},
		{"self-hosted host", false, false, "https://simple-hack.app", ""},
		{"self-hosted hack", false, true, "https://hack.example.com", ""},
	}
	for _, c := range cases {
		instanceIsHostedService = c.hosted
		kind := edition.Host
		if c.hack {
			kind = edition.Hack
		}
		assets := newAssetPipeline(edition.ForKind(kind), c.hackBase)
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
