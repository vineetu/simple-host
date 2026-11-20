package hackmcp

import (
	"github.com/vsriram/simple-host/host/mcp"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHackWhoAmIEmailOptIn(t *testing.T) {
	for _, args := range []map[string]any{{}, {"include_email": false}, {"include_email": true}} {
		t.Run(mcp.JSONText(args), func(t *testing.T) {
			up := http.HandlerFunc(jsonReply(200, `{"username":"person@example.test"}`))
			caller := mcp.Caller{APIKey: "personal", Mode: mcp.CallerModeEvents, GrantID: "grant"}
			s := newTestServer(up)
			r := httptest.NewRequest("POST", "/mcp", strings.NewReader(toolCall("who_am_i", args)))
			r.Header.Set("Content-Type", "application/json")
			r = r.WithContext(mcp.WithCaller(r.Context(), caller))
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			text, out, bad := resultOf(t, w)
			if bad {
				t.Fatal(text)
			}
			wantEmail := args["include_email"] == true
			_, hasEmail := out["email"]
			if hasEmail != wantEmail || strings.Contains(text, "person@example.test") != wantEmail {
				t.Fatalf("email disclosure mismatch: %s %v", text, out)
			}
			if wantEmail && out["email"] != "person@example.test" {
				t.Fatal("requested email missing")
			}
			if err := mcp.ValidateOutput(hackWhoAmITool().OutputSchema, out, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
