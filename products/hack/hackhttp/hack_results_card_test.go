package hackhttp

import (
	"strings"
	"testing"
)

// TestHackEventGalleryToolsAndResultAnchor is template-only: hackGalleryCard
// (hack_wire.go's hackGalleryCards, and HackEventPage's cross-reference with
// the results rows) gains Tools and ResultAnchor once results are published
// (build-plan §4.6).
func TestHackEventGalleryToolsAndResultAnchor(t *testing.T) {
	p := testHackEvent()
	p.Stage = "building"
	p.Gallery = []hackGalleryCard{
		{Title: "CareCompass", URL: "https://alpha.weekend.simple-hack.test/", Tools: []string{"Claude", "Canva"}, ResultAnchor: "#result-t1"},
		{Title: "NoResult", URL: "https://nr.weekend.simple-hack.test/"},
	}
	body := renderHackEvent(t, p).Body.String()
	cards := galleryCardHTML(body)
	if len(cards) != 2 {
		t.Fatalf("cards: %d, body:\n%s", len(cards), body)
	}
	if !strings.Contains(cards[0], `<li class="tag">Claude</li>`) || !strings.Contains(cards[0], `<li class="tag">Canva</li>`) {
		t.Fatalf("tool tags missing: %s", cards[0])
	}
	if !strings.Contains(cards[0], `href="#result-t1"`) {
		t.Fatalf("result anchor missing: %s", cards[0])
	}
	if strings.Contains(cards[1], "tags") || strings.Contains(cards[1], "site-result-link") {
		t.Fatalf("a team with no result should show no tags/anchor: %s", cards[1])
	}
}

// TestHackResultsCardEscaping is template-only (no DB): a hostile title and
// problem line must render as text, never executed, in both the open-card
// and collapsed-card branches of the results section (build-plan §9 point
// 6: "escaped ... html/template").
func TestHackResultsCardEscaping(t *testing.T) {
	p := testHackEvent()
	payload := `<script>alert(1)</script>"><img src=x onerror=1>`
	p.Results = &hackResultsView{
		FullRanking: true,
		Rows: []hackResultsRow{
			{TeamID: "t1", Rank: 1, TeamName: "Alpha", Overall: true, Card: &hackResultCard{
				Title: payload, Problem: payload, Tagline: "Solution line", Audience: "For " + payload,
				Tools: []string{payload}, Maturity: "working",
			}},
			{TeamID: "t2", Rank: 2, TeamName: "Beta", Card: &hackResultCard{
				Title: "Beta " + payload, Problem: "Beta problem",
			}},
		},
	}
	body := renderHackEvent(t, p).Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") || strings.Contains(body, `<img src=x onerror=1>`) {
		t.Fatalf("hostile entry text was not escaped:\n%s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatal("hostile text should still appear, escaped, as plain text")
	}
	if !strings.Contains(body, `id="result-t1"`) || !strings.Contains(body, `id="result-t2"`) {
		t.Fatal("result card anchors missing")
	}
	// t2 is not Overall/TrackWinner, so it is the collapsed <details> branch.
	if !strings.Contains(body, "<details") {
		t.Fatal("non-winner full-ranking row did not collapse into a details disclosure")
	}
}
