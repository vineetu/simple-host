package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/edition"
)

func TestSelfHostMarketingOnlyOnHostedServices(t *testing.T) {
	prev := InstanceIsHostedService
	t.Cleanup(func() { InstanceIsHostedService = prev })
	for _, tc := range []struct {
		name   string
		kind   *edition.Edition
		hosted bool
		base   string
	}{
		{"hosted Host", platformTestEdition(), true, "https://simple-hack.app"},
		{"self Host example.com", platformTestEdition(), false, "https://simple-hack.app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			InstanceIsHostedService = tc.hosted
			p := testAssetPipeline(tc.kind, tc.base)
			hosted := tc.hosted
			for _, page := range []struct{ name, marketing string }{
				// A product's pages are tested with the product (products/hack/servertest).
				{"features.html", `id="self-host"`}, {"host-story.html", "Self-host on one small server"},
			} {
				b, err := p.BuildChromePage(page.name, p.ChromeDataFor(httptest.NewRequest("GET", "/", nil), ""))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(b), page.marketing) != hosted {
					t.Errorf("%s marketing hosted=%v", page.name, hosted)
				}
				if !hosted && (strings.Contains(string(b), `href="/companies"`) || strings.Contains(string(b), "Self-host an event")) {
					t.Errorf("%s marketing navigation shown", page.name)
				}
			}
		})
	}
}
