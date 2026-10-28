package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHackWhoAmIEmailOptIn(t *testing.T) {
	for _, connection := range []string{"personal", "selected-team", "team-key"} {
		for _, args := range []map[string]any{{}, {"include_email": false}, {"include_email": true}} {
			t.Run(connection+"/"+jsonText(args), func(t *testing.T) {
				up := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body := `{"username":"person@example.test"}`
					if r.Header.Get("X-API-Key") == "team" {
						body = `{"team":{"event":"spring","team":"builders","site":"builders-site"}}`
					}
					jsonReply(200, body)(w, r)
				})
				caller := Caller{APIKey: "personal", Mode: CallerModeEvents, GrantID: "grant"}
				if connection == "selected-team" {
					caller.TeamAPIKey = "team"
				}
				if connection == "team-key" {
					caller.APIKey, caller.Mode = "team", CallerModeHackSite
				}
				s := newTestServer(up)
				r := httptest.NewRequest("POST", "/mcp", strings.NewReader(toolCall("who_am_i", args)))
				r.Header.Set("Content-Type", "application/json")
				r = r.WithContext(WithCaller(r.Context(), caller))
				w := httptest.NewRecorder()
				s.ServeHTTP(w, r)
				text, out, bad := resultOf(t, w)
				if bad {
					t.Fatal(text)
				}
				wantEmail := args["include_email"] == true && connection != "team-key"
				_, hasEmail := out["email"]
				if hasEmail != wantEmail || strings.Contains(text, "person@example.test") != wantEmail {
					t.Fatalf("email disclosure mismatch: %s %v", text, out)
				}
				if wantEmail && out["email"] != "person@example.test" {
					t.Fatal("requested email missing")
				}
				_, hasTeam := out["team"]
				if hasTeam != (connection != "personal") {
					t.Fatal("team context changed")
				}
				if err := ValidateOutput(hackWhoAmITool().OutputSchema, out, nil); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
