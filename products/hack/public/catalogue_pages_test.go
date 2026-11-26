package public

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/webassets"
)

// TestWriteEventPages is not a check: with SH_HACK_PAGES set to a folder it
// writes the public event page in every state and every Look there, for the
// web app's catalogue (make hack-web-pages). The clock is the catalogue's
// (web/hack/src/catalogue/fixtures.ts NOW), so countdowns and Now/Next marks
// stay put; links to the app are root-relative, so the catalogue serves them.
func TestWriteEventPages(t *testing.T) {
	dir := os.Getenv("SH_HACK_PAGES")
	if dir == "" {
		t.Skip("SH_HACK_PAGES not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	pageNow = func() time.Time { return now }
	defer func() { pageNow = time.Now }()
	pages := NewHackEventPages(webassets.Static)
	write := func(name string, p hackEventPage) {
		w := httptest.NewRecorder()
		renderHackEventPage(w, httptest.NewRequest("GET", "/", nil), p, pages)
		if err := os.WriteFile(filepath.Join(dir, name+".html"), w.Body.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, look := range []string{"", "poster", "confetti", "boardroom", "garden", "terminal"} {
		name := look
		if name == "" {
			name = "classic"
		}
		open := cataloguePage(now, "open")
		open.Theme = look
		write("event-"+name+"-open", open)
		res := cataloguePage(now, "results")
		res.Theme = look
		write("event-"+name+"-results", res)
	}
	for _, stage := range []string{"draft", "building", "judging"} {
		write("event-classic-"+stage, cataloguePage(now, stage))
	}
	winners := cataloguePage(now, "results")
	winners.Results = &hackResultsView{Rows: []hackResultsRow{
		{TeamID: "t1", Rank: 1, TeamName: "Lumbung", Overall: true, URL: "#result-t1", Shot: "/og-hack.png"},
		{TeamID: "t2", Rank: 1, TeamName: "Ojek AI", TrackName: "Getting around", TrackWinner: true, TrackPrize: "Rp 2,500,000"},
	}}
	write("event-classic-winners", winners)
	plain := hackEventPage{Slug: "quiet-night", Title: "Quiet Night", Stage: "open", TimeZone: "UTC", Color: "#267b79"}
	write("event-classic-bare", plain)
	gone := cataloguePage(now, "open")
	gone.TakenDown, gone.TakenDownReason, gone.Theme = true, "It broke the rules for public pages.", "poster"
	write("event-takendown", gone)
	w := httptest.NewRecorder()
	writeHackEventNotFound(w, httptest.NewRequest("GET", "/", nil), "", pages)
	if err := os.WriteFile(filepath.Join(dir, "event-notfound.html"), w.Body.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// cataloguePage is a full event: every section, at one stage.
func cataloguePage(now time.Time, stage string) hackEventPage {
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	p := hackEventPage{
		Slug: "jakarta-ai-night", Title: "Jakarta AI Night", Tagline: "Build something useful for your city in one weekend.",
		About:         "A weekend of making with AI tools, for anyone who lives or works in Jakarta.\n\nBring a laptop and an idea, or join a team on the night.",
		Rules:         "Teams of up to four.\nEverything you show must be built during the event.",
		Prizes:        "Overall winner: Rp 10,000,000.\nEach track winner: Rp 2,500,000.",
		OrganiserName: "Ayu", Organisation: "Jakarta Makers", Stage: stage, TimeZone: "Asia/Jakarta",
		StartsAt: now.Add(-24 * time.Hour), EndsAt: now.Add(30 * time.Hour), Participants: 48, Teams: 12,
		Color: "#d15d48",
		Tracks: []hackTrackView{
			{Name: "Food", Challenge: "Help street vendors and co-ops run their day.", Prize: "Rp 2,500,000"},
			{Name: "Getting around", Challenge: "Make the commute calmer.", Prize: ""},
		},
		Content: events.Content{
			Sponsors: []events.Sponsor{
				{Name: "Kopi Kenangan", Tier: "Gold", URL: "https://example.com/kopi", LogoData: "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 28 28'%3E%3Crect width='28' height='28' rx='6' fill='%23267b79'/%3E%3C/svg%3E"},
				{Name: "Gojek Labs", Tier: "", URL: ""},
			},
			FAQ: []events.FAQ{
				{Question: "Do I need a team?", Answer: "No. Come on your own and find one on the night,\nor ask the organisers to place you."},
				{Question: "Can I use any AI tool?", Answer: "Yes, any tool you like."},
			},
			Schedule: []events.ScheduleItem{
				{Title: "Doors open", Description: "Coffee, name tags and team finding.", StartAt: at(-2 * time.Hour), EndAt: at(-1 * time.Hour)},
				{Title: "Build", Description: "Heads down.", StartAt: at(-1 * time.Hour), EndAt: at(5 * time.Hour)},
				{Title: "Demos", StartAt: at(6 * time.Hour), EndAt: at(8 * time.Hour)},
			},
		},
		Announcements: []hackAnnouncementView{
			{Title: "Wi-Fi password", Body: "It is on the whiteboard by the door.", BodyHTML: "It is on the whiteboard by the door.", CreatedAt: now.Add(-30 * time.Minute), Pinned: true},
			{Title: "Lunch is here", Body: "Second floor.", BodyHTML: "Second floor.", CreatedAt: now.Add(-3 * time.Hour)},
			{Title: "Welcome", Body: "See you on Friday.", BodyHTML: "See you on Friday.", CreatedAt: now.Add(-72 * time.Hour)},
			{Title: "Tracks are out", Body: "Two tracks this year.", BodyHTML: "Two tracks this year.", CreatedAt: now.Add(-96 * time.Hour)},
		},
		Gallery: []hackGalleryCard{
			{Title: "Rice Ledger", Tagline: "A shared ledger farmers keep by voice message.", Team: "Lumbung", Shot: "/og-hack.png", URL: "https://lumbung.example/", Tools: []string{"Claude", "Sheets"}, TeamID: "t1"},
			{Title: "Ride Buddy", Team: "Ojek AI", URL: "https://ojek.example/", Initial: "R", TeamID: "t2"},
			{Title: "Kopi Club", Tagline: "Loyalty cards for warungs.", Team: "Kopi", URL: "https://kopi.example/", Initial: "K", TeamID: "t3"},
		},
	}
	if stage == "open" {
		p.ApplyURL = "/e/jakarta-ai-night/apply"
		p.Deadline = now.Add(50 * time.Hour)
		p.StartsAt, p.EndsAt = now.Add(48*time.Hour), now.Add(80*time.Hour)
	}
	if stage == "building" {
		p.Deadline = now.Add(26*time.Hour + 5*time.Minute)
	}
	if stage == "judging" || stage == "results" {
		p.Submissions = []hackSubmission{
			{hackResultCard: hackResultCard{Title: "Rice Ledger", Tagline: "A shared ledger farmers keep by voice message.", Problem: "Village co-ops lose track of who lent what.", Audience: "Rice co-ops", Tools: []string{"Claude", "Sheets"}, Maturity: "working", SiteURL: "https://lumbung.example/", VideoURL: "https://youtu.be/abc", VideoHost: "YouTube"}, TeamName: "Lumbung", ScreenshotURL: "/og-hack.png"},
			{hackResultCard: hackResultCard{Title: "Ride Buddy", Problem: "Drivers wait hours between rides.", Maturity: "concept", CodeURL: "https://github.com/example/ride", CodeHost: "GitHub"}, TeamName: "Ojek AI"},
		}
		p.VoteStatus = "open"
	}
	if stage == "results" {
		p.VoteStatus = "closed"
		card := func(title, problem, maturity string) *hackResultCard {
			return &hackResultCard{Title: title, Tagline: "One line on what it does.", Problem: problem, Audience: "People in Jakarta", Tools: []string{"Claude"}, Maturity: maturity, SiteURL: "https://example.com/", VideoURL: "https://youtu.be/abc", VideoHost: "YouTube"}
		}
		p.Results = &hackResultsView{FullRanking: true, Rows: []hackResultsRow{
			{TeamID: "t1", Rank: 1, TeamName: "Lumbung", Overall: true, OverallPrize: "Overall winner", Score: "86.50", Card: card("Rice Ledger", "Co-ops lose track.", "working"), Shot: "/og-hack.png"},
			{TeamID: "t2", Rank: 2, TeamName: "Ojek AI", TrackName: "Getting around", TrackWinner: true, TrackPrize: "Rp 2,500,000", Score: "80.25", Card: card("Ride Buddy", "Drivers wait.", "partial")},
			{TeamID: "t3", Rank: 3, TeamName: "Kopi", Score: "71.00", Tied: true, Card: card("Kopi Club", "Warungs lose regulars.", "concept")},
			{TeamID: "t4", Rank: 3, TeamName: "Batik Bot", Score: "71.00", Tied: true, Card: &hackResultCard{Title: "Batik Bot", Maturity: "concept"}},
		}}
	}
	return p
}
