package wpimport

import (
	"strings"
	"testing"
)

func TestImportRedirectsForChangedPermalinks(t *testing.T) {
	extra := bakeryPage("1", "Home", "/", "page", "<p>home</p>") + bakeryPage("123", "Plain", "/?p=123", "post", "<p>post</p>")
	r := convertWXR(t, bakeryChannel("en", "", "", "", extra), nil, nil)
	if got := string(r.Files["_redirects"]); !strings.Contains(got, "/ p=123 /p/123/ 301!\n") {
		t.Fatal(got)
	}
	r = convertWXR(t, bakeryChannel("en", "", "", "", bakeryPage("1", "Home", "/", "page", "home")+bakeryPage("2", "About", "/about/", "page", "about")), nil, nil)
	if len(r.Files["_redirects"]) != 0 {
		t.Fatal(string(r.Files["_redirects"]))
	}
}
