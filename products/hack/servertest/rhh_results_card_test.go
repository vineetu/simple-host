package servertest

import (
	"net/http"
	"strings"
	"testing"
)

// TestRhhResultsCards: the published card omits a code link the rules turn
// off, the snapshot holds both teams' cards, and the non-winner's card shows
// on public surfaces only once the full ranking is public (no republish).
func TestRhhResultsCards(t *testing.T) {
	w := newCardWorld(t)
	pub := w.publish(t)
	if pub.status != http.StatusOK {
		t.Fatalf("publish: %d %s", pub.status, pub.body)
	}
	text := string(pub.body)
	for _, want := range []string{"UNIQUE_ALPHA_PROBLEM", "UNIQUE_BETA_PROBLEM"} {
		if !strings.Contains(text, want) {
			t.Fatalf("snapshot lacks a card containing %s: %s", want, text)
		}
	}
	if strings.Contains(text, "github.com/alpha/repo") {
		t.Fatalf("snapshot carries a code link the rules turned off: %s", text)
	}

	base := "/v1/hack/events/" + w.slug
	surfaces := func(label string, betaShown bool) {
		t.Helper()
		for _, path := range []string{base + "/results", base + "/public"} {
			r := w.a.api(t, "GET", path, nil, "")
			if r.status != http.StatusOK {
				t.Fatalf("%s %s: %d %s", label, path, r.status, r.body)
			}
			body := string(r.body)
			if !strings.Contains(body, "UNIQUE_ALPHA_PROBLEM") || strings.Contains(body, "github.com/alpha/repo") {
				t.Fatalf("%s %s: winner card wrong: %s", label, path, body)
			}
			if path == base+"/results" || betaShown {
				if strings.Contains(body, "UNIQUE_BETA") != betaShown {
					t.Fatalf("%s %s: non-winner card shown=%v, want %v: %s", label, path, !betaShown, betaShown, body)
				}
			}
		}
		mine := w.a.api(t, "GET", base+"/my-results", nil, w.betaP.key).json(t)
		if mine["shown_publicly"] != betaShown {
			t.Fatalf("%s: non-winner shown_publicly=%v, want %v", label, mine["shown_publicly"], betaShown)
		}
	}
	surfaces("winners only", false)
	if r := w.a.api(t, "PATCH", base+"/results", map[string]any{"full_ranking": true}, w.org.key); r.status != http.StatusOK {
		t.Fatalf("full ranking: %d %s", r.status, r.body)
	}
	surfaces("full ranking", true)
}
