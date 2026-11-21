package hackhttp

import (
	"strings"
	"testing"
)

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
