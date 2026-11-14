package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/vsriram/simple-host/internal/testdb"
	"github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestChecklistMCPReplayAndSchemas(t *testing.T) {
	data := []byte(`{"can":{"manage":true,"judge":true,"take_part":false},"checklist":{"role":"organiser","stage":"draft","map_version":1,"items":[{"id":"o.entry-rules","label":"Choose what teams hand in","state":"todo","optional":false,"can_dismiss":false,"origin_stage":"draft","group":"current","actionable":true,"target_id":"o.entry-rules","link":{"screen":"organiser-settings","section":"settings","tab":"entries","href":"/e/spring/manage?s=settings&tab=entries#entry-rules"},"detail":"","count":null,"total":null,"settled":false,"blocked_reason":null}],"next":{"kind":"link","item_id":"o.entry-rules","target_id":"o.entry-rules","label":"Choose what teams hand in","link":{"screen":"organiser-settings","section":"settings","tab":"entries","href":"/e/spring/manage?s=settings&tab=entries#entry-rules"},"confirmation_required":false,"blocked_reason":null}}}`)
	var expected map[string]any
	_ = json.Unmarshal(data, &expected)
	for _, tc := range []struct {
		name, method, path string
		args               map[string]any
		body               string
	}{
		{"hack_get_checklist", "GET", "/v1/hack/events/spring/checklist", map[string]any{"slug": "spring"}, ""},
		{"hack_get_checklist", "GET", "/v1/hack/events/spring/checklist?role=judge", map[string]any{"slug": "spring", "role": "judge"}, ""},
		{"hack_set_checklist_not_needed", "POST", "/v1/hack/events/spring/checklist/not-needed/o.faq", map[string]any{"slug": "spring", "item_id": "o.faq", "not_needed": true}, ""},
		{"hack_set_checklist_not_needed", "DELETE", "/v1/hack/events/spring/checklist/not-needed/o.faq", map[string]any{"slug": "spring", "item_id": "o.faq", "not_needed": false}, ""},
		{"hack_acknowledge_checklist_item", "POST", "/v1/hack/events/spring/checklist/acknowledgements/j.rubric", map[string]any{"slug": "spring", "item_id": "j.rubric", "version": "current"}, `{"version":"current"}`},
		{"hack_acknowledge_checklist_item", "POST", "/v1/hack/events/spring/checklist/acknowledgements/j.conflicts", map[string]any{"slug": "spring", "item_id": "j.conflicts"}, `{}`},
	} {
		t.Run(tc.name+tc.method+tc.path, func(t *testing.T) {
			up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){}}
			// scriptUpstream keys by URL.Path; query values are asserted on the request.
			key := tc.path
			for i, c := range key {
				if c == '?' {
					key = key[:i]
					break
				}
			}
			up.ans[tc.method+" "+key] = func(w http.ResponseWriter, r *http.Request) {
				if r.URL.RequestURI() != tc.path {
					t.Errorf("URI %s", r.URL.RequestURI())
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(data)
			}
			server := newTestServer(up)
			text, out, bad := resultOf(t, sendMode(t, server, toolCall(tc.name, tc.args), CallerModeEvents))
			if bad {
				t.Fatal(text)
			}
			if !reflect.DeepEqual(out, expected) {
				t.Fatalf("REST/MCP differ: %s", text)
			}
			var tool Tool
			for _, v := range HackTools() {
				if v.Name == tc.name {
					tool = v
				}
			}
			if err := ValidateOutput(tool.OutputSchema, out, nil); err != nil {
				t.Fatal(err)
			}
			recordHackOutputValidated(tc.name)
			if up.last().body != tc.body {
				t.Fatalf("body %q", up.last().body)
			}
		})
	}
}

const hackChecklistFixtureJSON = `{"role":"organiser","stage":"draft","map_version":1,"items":[{"id":"o.entry-rules","label":"Choose what teams hand in","state":"todo","optional":false,"can_dismiss":false,"origin_stage":"draft","group":"current","actionable":true,"target_id":"o.entry-rules","link":{"screen":"organiser-settings","section":"settings","tab":"entries","href":"/e/spring/manage?s=settings&tab=entries#entry-rules"},"detail":"","count":null,"total":null,"settled":false,"blocked_reason":null,"version":"current"}],"next":{"kind":"link","item_id":"o.entry-rules","target_id":"o.entry-rules","label":"Choose what teams hand in","link":{"screen":"organiser-settings","section":"settings","tab":"entries","href":"/e/spring/manage?s=settings&tab=entries#entry-rules"},"confirmation_required":false,"blocked_reason":null,"stage":"open","deadline_passed":false,"cue":null}}`

// The replay contract is also checked against a real database computation,
// including the state change after an event-wide mark.
func TestChecklistDatabaseReplayParity(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	if os.Getenv("TESTDB_ISOLATE") == "1" {
		dsn = testdb.Fresh(t)
	}
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	slug := fmt.Sprintf("check-mcp-%d", time.Now().UnixNano())
	var actor, holding, eventID string
	if err = database.QueryRow(`INSERT INTO users(username) VALUES($1) RETURNING id`, slug+"-actor").Scan(&actor); err != nil {
		t.Fatal(err)
	}
	defer database.Exec(`DELETE FROM users WHERE id=$1`, actor)
	if err = database.QueryRow(`INSERT INTO users(username) VALUES($1) RETURNING id`, slug+"-holding").Scan(&holding); err != nil {
		t.Fatal(err)
	}
	defer database.Exec(`DELETE FROM users WHERE id=$1`, holding)
	if err = database.QueryRow(`INSERT INTO events(slug,account_id,created_by,title,organiser_name,contact_email,join_code,judge_code) VALUES($1,$2,$3,'Demo','Organiser','test@example.test',$4,$5) RETURNING id`, slug, holding, actor, slug+"-join", slug+"-judge").Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	defer database.Exec(`DELETE FROM events WHERE id=$1`, eventID)
	if _, err = database.Exec(`INSERT INTO event_members(event_id,user_id,role) VALUES($1,$2,'organiser')`, eventID, actor); err != nil {
		t.Fatal(err)
	}
	svc := events.NewChecklist(database, entries.New(database, nil), nil)
	path := "/v1/hack/events/" + slug + "/checklist"
	up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){}}
	var rest map[string]any
	up.ans["GET "+path] = func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.Read(ctx, eventID, actor, "organiser", false, nil, time.Now())
		if err != nil {
			t.Error(err)
			http.Error(w, "read", 500)
			return
		}
		raw, _ := json.Marshal(map[string]any{"can": events.AbilitiesForRole("organiser"), "checklist": list})
		_ = json.Unmarshal(raw, &rest)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}
	server := newTestServer(up)
	for _, marked := range []bool{false, true} {
		if marked {
			if err = svc.Write(ctx, eventID, actor, "o.faq", "", &marked); err != nil {
				t.Fatal(err)
			}
		}
		text, out, bad := resultOf(t, sendMode(t, server, toolCall("hack_get_checklist", map[string]any{"slug": slug}), CallerModeEvents))
		if bad {
			t.Fatal(text)
		}
		if !reflect.DeepEqual(out, rest) {
			t.Fatal("REST/MCP checklist items or next differ")
		}
		for _, tool := range HackTools() {
			if tool.Name == "hack_get_checklist" {
				if err = ValidateOutput(tool.OutputSchema, out, nil); err != nil {
					t.Fatal(err)
				}
				recordHackOutputValidated(tool.Name)
			}
		}
	}
}
