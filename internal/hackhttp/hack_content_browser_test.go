package hackhttp

import (
	"io/fs"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/webassets"
)

// TestServeHackContentForBrowser is an opt-in local fixture for mobile theme checks.
func TestServeHackContentForBrowser(t *testing.T) {
	addr := os.Getenv("HACK_CONTENT_SERVE_ADDR")
	if addr == "" {
		t.Skip("HACK_CONTENT_SERVE_ADDR unset")
	}
	p := testHackEvent()
	p.AppURL = "http://" + addr
	p.TimeZone = "America/New_York"
	p.Content = hackContent{
		Sponsors: []hackSponsor{{Name: "Community sponsor", Tier: "Gold", URL: "https://example.com", LogoData: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL/nwAAAABJRU5ErkJggg=="}},
		FAQ:      []hackFAQ{{Question: "Can I join?", Answer: "Yes, bring your ideas."}},
		Schedule: []hackScheduleItem{
			{Title: "Opening", StartAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), EndAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)},
			{Title: "Demo", StartAt: time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339), EndAt: time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339)},
		},
	}
	p.Announcements = []hackAnnouncementView{{Title: "Welcome", Body: "Build something kind.", CreatedAt: time.Now()}}
	p.Deadline = time.Now().Add(25 * time.Hour)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// SIM-353 review verification: ?theme=poster|confetti|boardroom|garden|terminal
		// (or no param / theme=) switches the Look so one running fixture can
		// serve every theme and Classic without a restart or a real DB.
		page := p
		page.Theme = r.URL.Query().Get("theme")
		renderHackEventPage(w, r, page, testHackPages)
	})
	mux.HandleFunc("/site.css", func(w http.ResponseWriter, r *http.Request) {
		css, err := fs.ReadFile(webassets.Static, "static/site.css")
		if err != nil {
			http.Error(w, "missing CSS", 500)
			return
		}
		w.Header().Set("Content-Type", "text/css")
		_, _ = w.Write(css)
	})
	// SIM-353 review verification: the page links kit.css, public.css and
	// (when themed) themes.css under AppURL/hack/ -- without these the
	// fixture rendered unstyled HTML (every background computed as
	// transparent, which is how the missing full-bleed fix went unnoticed).
	for _, name := range []string{"kit.css", "public.css", "themes.css"} {
		name := name
		mux.HandleFunc("/hack/"+name, func(w http.ResponseWriter, r *http.Request) {
			css, err := fs.ReadFile(webassets.Static, "static/hack/"+name)
			if err != nil {
				http.Error(w, "missing CSS", 500)
				return
			}
			w.Header().Set("Content-Type", "text/css")
			_, _ = w.Write(css)
		})
	}
	t.Fatal(http.ListenAndServe(addr, mux))
}
