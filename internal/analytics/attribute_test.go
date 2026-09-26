package analytics

import "testing"

func TestAttributeHosts(t *testing.T) {
	i := NewIngester(nil, "", "s", "sites.example.app", "example.app")
	m := &attrMaps{
		handleToUser: map[string]string{"olive": "u1"},
		userNameToID: map[string]string{"u1/shop": "s-shop", "u1/blog": "s-blog"},
		nameToOldest: map[string]string{"legacy": "s-legacy", "shop": "s-other-shop"},
		domainToID:   map[string]string{"clay.example.app": "s-clay", "brand.com": "s-brand"},
	}
	for _, tc := range []struct{ host, uri, want string }{
		{"sites.example.app", "/olive/shop/", "s-shop"},
		{"olive.example.app", "/shop/", "s-shop"},
		{"olive.example.app", "/blog/post.html?x=1", "s-blog"},
		{"olive.example.app", "/", ""},
		{"olive.example.app", "/nope/", ""},
		{"clay.example.app", "/", "s-clay"},
		{"legacy.example.app", "/", "s-legacy"},
		{"brand.com", "/", "s-brand"},
		{"shop.olive.example.app", "/", "s-shop"},
		{"blog.olive.example.app", "/post.html?x=1", "s-blog"},
		{"nope.olive.example.app", "/", ""},
		{"shop.nobody.example.app", "/", ""},
		{"a.b.c.example.app", "/", ""},
	} {
		if got := i.attribute(tc.host, tc.uri, m); got != tc.want {
			t.Errorf("%s%s: got %q want %q", tc.host, tc.uri, got, tc.want)
		}
	}
}
