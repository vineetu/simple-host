package handler

import "testing"

func TestRewriterKeepsPathModelOnOtherInstances(t *testing.T) {
	rw := newHostRewriter("event.example", "", "")
	for in, want := range map[string]string{
		"https://<handle>.simple-host.app/<site>/":                 "https://sites.event.example/<handle>/<site>/",
		"https://<handle>.simple-host.app/<sitename>/x":            "https://sites.event.example/<handle>/<sitename>/x",
		"https://<handle>.simple-host.app/v1/sites/<name>":         "https://sites.event.example/v1/sites/<name>",
		"https://<handle>.simple-host.app/":                        "https://sites.event.example/<handle>/",
		"<code>&lt;handle&gt;.simple-host.app</code>":              "<code>sites.event.example/&lt;handle&gt;</code>",
		"https://sites.simple-host.app/<handle>/<site>/ old":       "https://sites.event.example/<handle>/<site>/ old",
		"https://simple-host.app/auth.js":                          "https://event.example/auth.js",
		"https://<site>.<handle>.simple-host.app/":                 "https://sites.event.example/<handle>/<site>/",
		"https://<site>.<handle>.simple-host.app/x.html":           "https://sites.event.example/<handle>/<site>/x.html",
		"<code>&lt;site&gt;.&lt;handle&gt;.simple-host.app</code>": "<code>sites.event.example/&lt;handle&gt;/&lt;site&gt;</code>",
	} {
		if got := string(rw.apply([]byte(in))); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}
