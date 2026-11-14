package servertest

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/products/hack/hackhttp"
)

func TestHackathonsPage(t *testing.T) {
	for _, base := range []string{"https://simple-hack.app", "https://hack.example.test"} {
		t.Run(base, func(t *testing.T) {
			sh := chromeTestHandlerWithBase(edition.Hack, base)
			mux := http.NewServeMux()
			handler.RegisterUIRoutes(mux, base, sh)
			hackhttp.RegisterHackHome(mux, sh.Pages())
			for _, path := range []string{"/hackathons", "/hackathons.html"} {
				rec := get(t, mux, strings.TrimPrefix(base, "https://"), path)
				if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Hackathons, now for everyone.") {
					t.Fatalf("%s = %d, missing hackathons page", path, rec.Code)
				}
				if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Content-Security-Policy") == "" {
					t.Errorf("%s lost page headers", path)
				}
				if strings.Contains(rec.Body.String(), "<!--sh:") {
					t.Errorf("%s has unrendered chrome", path)
				}
			}
		})
	}
}

func TestHackSelfHostingLinksReachInstructions(t *testing.T) {
	sh := chromeTestHandlerWithBase(edition.Hack, "https://simple-hack.app")
	mux := http.NewServeMux()
	hackhttp.RegisterHackHome(mux, sh.Pages())
	links := regexp.MustCompile(`href="([^"]+)"[^>]*>(Self-host[^<]*|For hackathons)</a>`)
	for _, page := range []struct {
		file  string
		links int
	}{
		{"hack-home.html", 2},
		{"hack-story.html", 2},
		{"hack-get-started.html", 2},
		{"enterprise.html", 2},
		{"features.html", 3},
	} {
		t.Run(page.file, func(t *testing.T) {
			rec := get(t, sh.Pages().ServeStaticPage(page.file), "simple-hack.app", "/"+page.file)
			if rec.Code != http.StatusOK {
				t.Fatalf("page status = %d", rec.Code)
			}
			matches := links.FindAllStringSubmatch(rec.Body.String(), -1)
			if len(matches) != page.links {
				t.Fatalf("found %d self-host links, want %d", len(matches), page.links)
			}
			for _, match := range matches {
				u, err := url.Parse(match[1])
				if err != nil {
					t.Fatal(err)
				}
				if u.Scheme != "https" || u.Host != "simple-hack.app" || u.Path != "/hackathons" {
					t.Fatalf("%s links to %q, want Hack's self-hosting page", match[2], match[1])
				}
				destination := get(t, mux, u.Host, u.RequestURI())
				if destination.Code != http.StatusOK || !strings.Contains(destination.Body.String(), "Choose your installation") || !strings.Contains(destination.Body.String(), "simple-hack-standalone.md") {
					t.Errorf("%s destination = %d, missing installation instructions", match[2], destination.Code)
				}
			}
		})
	}
}
