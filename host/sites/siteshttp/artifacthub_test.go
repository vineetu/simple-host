package siteshttp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/web"
)

// Named arrivals keep the landing film and carry attribution without cookies.
func TestNamedSourceArrival(t *testing.T) {
	mux := chromeTestMux(t, platformTestEdition())

	for _, source := range []string{"artifact-hub", "vercel-ship", "agentcon"} {
		rec := get(t, mux, "simple-host.app", "/?from="+source)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, `<video id="film"`) {
			t.Fatalf("home with from=%s: %d", source, rec.Code)
		}
		if strings.Contains(body, "title: 'Artifact Hub") {
			t.Error("the shared film's page title must say Simple Host")
		}
		for _, want := range []string{`'` + source + `': 'everything'`, `file: 'artifact-film'`, `sessionStorage.setItem('sh-film', key)`, `Simple Host | Everything you make with AI, in one place`} {
			if !strings.Contains(body, want) {
				t.Errorf("home page for from=%s missing %s", source, want)
			}
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatal("landing wrote a cookie")
		}
		if !strings.Contains(body, "window.SHArrival") {
			t.Fatal("no same-visit navigation helper")
		}

	}
	for _, path := range []string{"/", "/?from=%3Cb%3E"} {
		rec := get(t, mux, "simple-host.app", path)
		if len(rec.Result().Cookies()) != 0 {
			t.Errorf("%s set a cookie", path)
		}
	}

	for name, ctype := range web.ArtifactFilmTypes {
		rec := get(t, mux, "simple-host.app", "/film/"+name+"?v=20261015")
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != ctype || rec.Body.Len() == 0 || rec.Header().Get("ETag") == "" {
			t.Fatalf("%s: %d %q %d", name, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
		}
	}
}
