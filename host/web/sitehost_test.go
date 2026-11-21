package web

import (
	"testing"

	"github.com/vsriram/simple-host/host/sites"
)

func TestSiteHostLabels(t *testing.T) {
	for host, want := range map[string]string{
		"shop.olive.example.app":   "shop/olive",
		"SHOP.Olive.example.app.":  "shop/olive",
		"olive.example.app":        "",
		"a.b.c.example.app":        "",
		"example.app":              "",
		".olive.example.app":       "",
		"shop..example.app":        "",
		"shop.olive.other.app":     "",
		"shop.olive.example.app.x": "",
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
