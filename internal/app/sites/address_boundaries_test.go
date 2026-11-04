package sites

import (
	"testing"

	rules "github.com/vsriram/simple-host/internal/rules/sites"
)

func TestLegacyHostLabel(t *testing.T) {
	for _, base := range []string{"simple-host.app", "sh-site.test"} {
		for _, label := range []string{"shop", "a-b", "a.b", "", "xn--shop"} {
			host := label + "." + base
			got, ok := LegacyHostLabel(host, base)
			want := label != "" && label != "a.b"
			if ok != want || (ok && got != label) {
				t.Errorf("LegacyHostLabel(%q, %q) = %q, %v", host, base, got, ok)
			}
		}
		for label := range rules.ReservedSubdomainSet {
			if got, ok := LegacyHostLabel(label+"."+base, base); ok || got != "" {
				t.Errorf("reserved %q under %q routed to %q", label, base, got)
			}
		}
		for _, host := range []string{base, "shop.example.com", "shop." + base + "."} {
			if got, ok := LegacyHostLabel(host, base); ok || got != "" {
				t.Errorf("unmatched %q under %q routed to %q", host, base, got)
			}
		}
	}
}
