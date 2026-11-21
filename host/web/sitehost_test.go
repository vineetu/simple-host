package web

import (
	"testing"

	"github.com/vsriram/simple-host/host/sites"
)

func TestSiteHostLabels(t *testing.T) {
	for host, want := range map[string]string{
		"shop.olive.example.app":  "shop/olive",
		"SHOP.Olive.example.app.": "shop/olive",
		"olive.example.app":       "",
		"shop.olive.other.app":    "",
	} {
		s, h, ok := sites.SiteHostLabels(host, "example.app")
		got := ""
		if ok {
			got = s + "/" + h
		}
		if got != want {
			t.Errorf("%q: got %q want %q", host, got, want)
		}
	}
}
