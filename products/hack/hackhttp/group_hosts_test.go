package hackhttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/model"
)

func TestGroupHostsPagePaths(t *testing.T) {
	for _, path := range []string{"/", "/screenshots/alpha", "/screenshots/alpha--beta", "/screenshots/" + strings.Repeat("a", 30), "/screenshots/" + strings.Repeat("a", 31), "/screenshots/Alpha", "/screenshots/-alpha", "/screenshots/alpha/other", "/screenshots/../alpha", "/other"} {
		page, shot := "", ""
		g := NewGroupHosts(nil, func(w http.ResponseWriter, r *http.Request, u model.User) bool { page = "page"; return true }, func(w http.ResponseWriter, r *http.Request, u model.User, s string) bool { shot = s; return true })
		got := g.ServePage(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil), model.User{})
		slug, valid := hackScreenshotPath(path)
		want := path == "/" || valid
		if got != want || (path == "/" && page != "page") || (valid && shot != slug) {
			t.Fatalf("%s: got %v page=%s shot=%s", path, got, page, shot)
		}
	}
}
