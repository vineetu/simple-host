package assistant

import (
	"strings"
	"testing"
)

// TestAssistantTakenDownEventRefuses: a taken-down event's organiser gets
// 403 event_taken_down from both routes, without ever reaching the
// provider (adversarial review, SIM-394) -- loadMember only applies this
// check for write=true, and the assistant route calls it with write=false
// (it needs the plain, non-404-masking loadMember behaviour for its own
// 403 organisers_only), so assistantAccess checks TakenDown itself.
// TestSanitizeAssistantEventViewJSONCatchesTypedValues: the sanitiser's
// recursion only matches generic map[string]any/[]any shapes, so a value
// built from concrete Go types -- a typed struct, a []map[string]any --
// would otherwise pass straight through untouched. The marshal/unmarshal
// round trip (adversarial review, SIM-394) canonicalises everything into
// those generic shapes first, so a secret-looking field name is caught no
// matter what Go type originally carried it.
func TestSanitizeAssistantEventViewJSONCatchesTypedValues(t *testing.T) {
	type needsYouRow struct {
		JoinCode     string `json:"join_code"`
		ContactEmail string `json:"contact_email"`
		Label        string `json:"label"`
	}
	type eventViewLike struct {
		NeedsYou []needsYouRow  `json:"needs_you"`
		Counts   map[string]int `json:"counts"`
		Title    string         `json:"title"`
	}
	view := eventViewLike{
		NeedsYou: []needsYouRow{{JoinCode: "ABC123", ContactEmail: "a@b.c", Label: "Approve Pat"}},
		Counts:   map[string]int{"teams": 3},
		Title:    "Spring hack",
	}
	raw, err := sanitizeAssistantEventViewJSON(view)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, "ABC123") || strings.Contains(s, "join_code") {
		t.Fatalf("join_code leaked from a typed struct field: %s", s)
	}
	if strings.Contains(s, "a@b.c") || strings.Contains(s, "contact_email") {
		t.Fatalf("contact_email leaked from a typed struct field: %s", s)
	}
	if !strings.Contains(s, "Approve Pat") || !strings.Contains(s, "Spring hack") || !strings.Contains(s, `"teams":3`) {
		t.Fatalf("non-secret fields were dropped too: %s", s)
	}
}
