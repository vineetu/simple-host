package siteshttp

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/sites/sitesroute"
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
		if got := sitesroute.VersionAuthor(r); got != c.want {
			t.Errorf("sitesroute.VersionAuthor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := sitesroute.VersionAuthor(httptest.NewRequest("PUT", "/v1/sites/s/files", nil)); got != "" {
		t.Errorf("no ?author=: %q, want empty", got)
	}
}
