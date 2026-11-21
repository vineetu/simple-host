package siteshttp

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestVersionAuthor(t *testing.T) {
	long := strings.Repeat("é", 100)
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"  Alice  ", "Alice"},
		{"Bo\x01b\x7f", "Bob"},
		{long, strings.Repeat("é", 80)},
	} {
		r := httptest.NewRequest("PUT", "/v1/sites/s/files?"+url.Values{"author": {c.in}}.Encode(), nil)
		if got := versionAuthor(r); got != c.want {
			t.Errorf("versionAuthor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := versionAuthor(httptest.NewRequest("PUT", "/v1/sites/s/files", nil)); got != "" {
		t.Errorf("no ?author=: %q, want empty", got)
	}
}
