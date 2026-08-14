package handler

import "testing"

func TestParseAbsoluteReturnTo(t *testing.T) {
	httpsBase := "https://simple-host.app"
	httpBase := "http://localhost:8090"
	cases := []struct {
		name    string
		raw     string
		base    string
		wantErr bool
	}{
		{name: "https content host", raw: "https://sites.simple-host.app/alice/blog/", base: httpsBase},
		{name: "http allowed when public base is http", raw: "http://sites.simple-host.app/alice/blog/", base: httpBase},
		{name: "http rejected when public base is https", raw: "http://sites.simple-host.app/alice/blog/", base: httpsBase, wantErr: true},
		{name: "evil host still parses as URL", raw: "https://evil.example/phish", base: httpsBase},
		{name: "userinfo rejected", raw: "https://user:pass@sites.simple-host.app/alice/blog/", base: httpsBase, wantErr: true},
		{name: "scheme-relative rejected", raw: "//evil.example/phish", base: httpsBase, wantErr: true},
		{name: "relative rejected", raw: "/alice/blog/", base: httpsBase, wantErr: true},
		{name: "missing return_to", raw: "", base: httpsBase, wantErr: true},
		{name: "javascript scheme", raw: "javascript:alert(1)", base: httpsBase, wantErr: true},
		{name: "non-default https port", raw: "https://sites.simple-host.app:8443/alice/blog/", base: httpsBase, wantErr: true},
		{name: "default https port ok", raw: "https://sites.simple-host.app:443/alice/blog/", base: httpsBase},
		{name: "default http port ok", raw: "http://sites.simple-host.app:80/alice/blog/", base: httpBase},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseAbsoluteReturnTo(c.raw, c.base)
			if c.wantErr && err == nil {
				t.Fatalf("expected error")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestSplitContentHostPath(t *testing.T) {
	ok := func(p, handle, site string) {
		t.Helper()
		h, s, good := splitContentHostPath(p)
		if !good || h != handle || s != site {
			t.Fatalf("path %q -> (%q,%q,%v), want (%q,%q,true)", p, h, s, good, handle, site)
		}
	}
	bad := func(p string) {
		t.Helper()
		if h, s, good := splitContentHostPath(p); good {
			t.Fatalf("path %q should be rejected, got %s/%s", p, h, s)
		}
	}
	ok("/alice/blog/", "alice", "blog")
	ok("/alice/blog", "alice", "blog")
	ok("/alice/blog/index.html", "alice", "blog")
	ok("/alice/blog/foo/bar", "alice", "blog")
	bad("/")
	bad("/alice")
	bad("/alice/")
	bad("/alice/blog/../../../")
	bad("/ALICE/blog/")
	bad("//evil")
	bad("/alice/ThisIsWayTooLongForASitenameBecauseItExceedsSixtyThreeCharsXXXX/")
}

func TestIsRejectedPlatformHost(t *testing.T) {
	reject := []string{"simple-host.app", "www.simple-host.app", "blog.simple-host.app", "localhost"}
	for _, h := range reject {
		if !isRejectedPlatformHost(h, "simple-host.app", "sites.simple-host.app", "localhost") {
			t.Fatalf("expected reject %q", h)
		}
	}
	if isRejectedPlatformHost("sites.simple-host.app", "simple-host.app", "sites.simple-host.app", "localhost") {
		t.Fatalf("content host must not be rejected")
	}
	if isRejectedPlatformHost("recipes.brand.com", "simple-host.app", "sites.simple-host.app", "simple-host.app") {
		t.Fatalf("custom domain must not be rejected as platform host")
	}
}
