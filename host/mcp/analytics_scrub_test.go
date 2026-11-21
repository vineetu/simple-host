package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// Free text the model writes (the stated intent) loses links, codes, email
// addresses, raw IDs and keys it may repeat from an earlier call; ordinary
// words and plain links stay.
func TestScrubAnalyticsText(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"The organiser shares the co-organiser link https://simple-hack.app/organiser/k7m2p9q4r8s3t6v5w2x9y4z7a3b8c5d6 with a colleague.", "The organiser shares the co-organiser link [link removed] with a colleague."},
		{"Joins with https://simple-hack.app/join/ab23cd45 after reading it.", "Joins with [link removed] after reading it."},
		{"Opens the team link simple-hack.app/team/qr7x2m9k for the teammate.", "Opens the team link simple-hack.app/team/[code removed] for the teammate."},
		{"Uses the judge code AB23CD45XY to join.", "Uses the judge code [code removed] to join."},
		{"Invites jane.doe+hack@example.co.uk as a co-organiser.", "Invites [email removed] as a co-organiser."},
		{"Removes user 3f2a9c1e-5b6d-4e7f-8a9b-0c1d2e3f4a5b from the team.", "Removes user [id removed] from the team."},
		{"Tries key shk_0123456789abcdef0123456789abcdef again.", "Tries key [removed] again."},
		{"Downloads https://sites.simple-host.app/x?sig=abc from storage.", "Downloads [link removed] from storage."},
		{"Checks https://blue-fox.spring-jam.simple-hack.app/ before judging opens in 2026.", "Checks https://blue-fox.spring-jam.simple-hack.app/ before judging opens in 2026."},
		{"The organiser reviews everybody's submissions before the deadline.", "The organiser reviews everybody's submissions before the deadline."},
	} {
		if got := scrubAnalyticsText(tc.input); got != tc.want {
			t.Errorf("scrub(%q)\n got %q\nwant %q", tc.input, got, tc.want)
		}
	}
}

func scrubTestServer(t *testing.T, run func(*call, map[string]any) (output, error)) *Server {
	t.Helper()
	s := newTestServer(&recordingUpstream{})
	s.analytics = &analyticsSender{queue: make(chan analyticsEvent, 1)}
	s.byName["scrub_test"] = Tool{Name: "scrub_test", InputSchema: object(map[string]any{"slug": str("Event."), "owner": str("Owner.")}), run: run}
	return s
}

func scrubTestEvent(t *testing.T, s *Server, args map[string]any) analyticsEvent {
	t.Helper()
	var req request
	if err := json.Unmarshal([]byte(toolCall("scrub_test", args)), &req); err != nil {
		t.Fatal(err)
	}
	s.callTool(httptest.NewRequest("POST", "/mcp", nil), req, Caller{AppName: "Claude", UserID: "u1"}, false)
	select {
	case e := <-s.analytics.queue:
		return e
	default:
		t.Fatal("no analytics event")
	}
	return analyticsEvent{}
}

// A refused call sends its refusal code and a fixed message, never the
// error text, which can echo what the caller sent; the arguments and the
// intent lose the email address too.
func TestAnalyticsRefusedCallSendsOnlyTheCode(t *testing.T) {
	const email = "jane.private@example.com"
	ran := false
	s := scrubTestServer(t, func(*call, map[string]any) (output, error) { ran = true; return output{Text: "ok"}, nil })
	e := scrubTestEvent(t, s, map[string]any{"slug": "spring", email: true, "context": "Asks about " + email + " joining with https://simple-hack.app/join/ab23cd45 today."})
	raw, _ := json.Marshal(e)
	if ran || strings.Contains(string(raw), email) || strings.Contains(string(raw), "ab23cd45") {
		t.Fatalf("refused call leaked: %s", raw)
	}
	if e.Error == nil || e.Error.Message != "invalid_argument" || !strings.Contains(string(raw), "Refused: invalid_argument.") {
		t.Fatalf("refusal code missing: %s", raw)
	}

	// A failed run also sends only its code.
	s = scrubTestServer(t, func(*call, map[string]any) (output, error) {
		return output{}, codedError("event_not_found", "No event "+email)
	})
	e = scrubTestEvent(t, s, map[string]any{"slug": "spring"})
	raw, _ = json.Marshal(e)
	if strings.Contains(string(raw), email) || e.Error.Message != "event_not_found" || !strings.Contains(string(raw), "Refused: event_not_found.") {
		t.Fatalf("failed run: %s", raw)
	}
	s = scrubTestServer(t, func(*call, map[string]any) (output, error) { return output{}, errors.New("bad " + email) })
	if raw, _ = json.Marshal(scrubTestEvent(t, s, map[string]any{"slug": "spring"})); strings.Contains(string(raw), email) || !strings.Contains(string(raw), "Refused: tool_error.") {
		t.Fatalf("uncoded failure: %s", raw)
	}
}

// The product's tags see the owner a team website call named, although the
// call itself takes owner off its arguments before the tool runs.
func TestAnalyticsProductTagsSeeTheOwner(t *testing.T) {
	var seen map[string]any
	s := scrubTestServer(t, func(_ *call, args map[string]any) (output, error) {
		if _, has := args["owner"]; has {
			t.Error("owner reached the tool")
		}
		return output{Text: "ok"}, nil
	})
	s.cfg.Contribution = &Contribution{AnalyticsTags: func(_ context.Context, _ Caller, _ string, args map[string]any) map[string]string {
		seen = args
		return nil
	}}
	e := scrubTestEvent(t, s, map[string]any{"owner": "Spring-Jam"})
	if e.lateTags == nil {
		t.Fatal("no product tags")
	}
	e.lateTags(context.Background())
	if seen["owner"] != "spring-jam" {
		t.Fatalf("tags saw %v", seen)
	}
}

// A tool can keep accepting arguments its schema no longer lists.
func TestAcceptUnlistedArguments(t *testing.T) {
	tool := AcceptUnlisted(Tool{Name: "storage_sql_execute", InputSchema: object(map[string]any{"sql": str("SQL.")}, "sql")}, "table", "values")
	if err := unexpectedArgument(tool, map[string]any{"sql": "SELECT 1", "table": "rows", "values": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if err := unexpectedArgument(tool, map[string]any{"sql": "SELECT 1", "tabel": "rows"}); err == nil {
		t.Fatal("an argument never listed was accepted")
	}
}
