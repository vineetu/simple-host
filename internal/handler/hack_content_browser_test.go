package handler

import (
	"net/http"
	"os"
	"testing"
	"time"
)

// TestServeHackContentForBrowser is an opt-in local fixture for mobile theme checks.
func TestServeHackContentForBrowser(t *testing.T) {
	addr := os.Getenv("HACK_CONTENT_SERVE_ADDR")
	if addr == "" {
		t.Skip("HACK_CONTENT_SERVE_ADDR unset")
	}
	SetHackChrome(true)
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
	p.Announcements = []hackAnnouncementView{{Title: "Welcome", Body: "Build something kind.", When: "Today"}}
	p.Deadline = time.Now().Add(25 * time.Hour)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { renderHackEventPage(w, r, p) })
	mux.HandleFunc("/site.css", func(w http.ResponseWriter, r *http.Request) {
		css, err := staticFiles.ReadFile("static/site.css")
		if err != nil {
			http.Error(w, "missing CSS", 500)
			return
		}
		w.Header().Set("Content-Type", "text/css")
		_, _ = w.Write(css)
	})
	t.Fatal(http.ListenAndServe(addr, mux))
}
