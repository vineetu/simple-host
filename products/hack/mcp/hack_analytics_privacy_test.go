package hackmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/mcp"
)

// What the privacy page promises AgentCat never gets from a Simple Hack call:
// join, judge and co-organiser codes and links, email addresses, other
// people's names and account IDs, and applicants' sign-up answers, city,
// country, age range and gender. These tests define those classes by key
// shape, independently of the filter, so a new tool or REST field of a known
// class fails here until the filter removes it.

const leak = "PLANTED_PRIVATE_VALUE"

// keptNameKeys are *_name keys that name no person.
var keptNameKeys = map[string]bool{
	"team_name": true, "track_name": true, "event_name": true, "file_name": true, "filename": true,
	"site_name": true, "resource_name": true, "tool_name": true, "client_name": true, "prize_name": true,
	"criterion_name": true, "confirm_name": true, "server_name": true, "hostname": true, "cname": true,
	"table_name": true, "column_name": true,
}

var (
	personIDKey = regexp.MustCompile(`^(me_id|.*(user|account|judge|member|owner|organiser|sender|author|creator|successor|person|applicant|participant|voter)_?(user_)?ids?|.+_by|organiser_judges|former_judges|organisers_staying|others)$`)
	contactKey  = regexp.MustCompile(`email|phone`)
	codeKey     = regexp.MustCompile(`(^|_)code$|^(join|judge|invite|team)_(url|link)$|^link$`)
	freeTextKey = regexp.MustCompile(`^(answers|city|country|age_range|gender)$`)
)

// promisedClass names the class of a key the copy must never carry a value
// for, or "" for a key that may stay.
func promisedClass(key string) string {
	k := strings.ToLower(key)
	switch {
	case freeTextKey.MatchString(k):
		return "applicant free text"
	case codeKey.MatchString(k):
		return "code or link"
	case contactKey.MatchString(k) && k != "email_updates" && k != "include_emails" && k != "email_participants" && k != "emails_sent" && !strings.HasPrefix(k, "emailable"):
		return "contact"
	case personIDKey.MatchString(k):
		return "person ID"
	case k == "display_name" || k == "username" || k == "names" || (strings.HasSuffix(k, "_name") || strings.HasSuffix(k, "_names")) && !keptNameKeys[k]:
		return "person name"
	}
	return ""
}

// leaks walks a telemetry copy (JSON text blocks decoded) and reports each
// planted value under a promised class, or under a person's name or id.
func leaks(value any, path string, person bool, out *[]string) {
	switch v := value.(type) {
	case string:
		if strings.Contains(v, leak) {
			var inner any
			if json.Unmarshal([]byte(v), &inner) == nil {
				leaks(inner, path, person, out)
				return
			}
		}
	case []any:
		for _, item := range v {
			leaks(item, path+"[]", person, out)
		}
	case map[string]any:
		isPerson := false
		for _, signal := range []string{"user_id", "account_id", "judge_id", "judge_user_id", "member_id", "email", "display_name", "username"} {
			if _, ok := v[signal]; ok {
				isPerson = true
			}
		}
		for key, child := range v {
			class := promisedClass(key)
			if class == "" && (key == "name" || key == "id") && (isPerson || person) {
				class = "person " + key
			}
			if class != "" && strings.Contains(mustJSON(child), leak) {
				*out = append(*out, path+"."+key+" ("+class+")")
				continue
			}
			leaks(child, path+"."+key, personCollection(key), out)
		}
	}
}

// personCollection reports a key whose objects are people.
func personCollection(key string) bool {
	switch key {
	case "members", "judges", "former_judges", "people", "participants", "organisers", "successors",
		"admins", "applicants", "user", "creator", "sender", "author", "me", "judge", "organiser", "person":
		return true
	}
	return false
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// sampleFromSchema fills a schema with the planted value, keyed by field.
func sampleFromSchema(schema map[string]any, depth int) any {
	if depth > 8 || schema == nil {
		return leak
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		out := map[string]any{}
		for key, raw := range props {
			child, _ := raw.(map[string]any)
			out[key] = sampleFromSchema(child, depth+1)
		}
		return out
	}
	if items, ok := schema["items"].(map[string]any); ok {
		return []any{sampleFromSchema(items, depth+1)}
	}
	for _, combo := range []string{"oneOf", "anyOf", "allOf"} {
		if options, ok := schema[combo].([]any); ok && len(options) > 0 {
			if first, ok := options[0].(map[string]any); ok {
				return sampleFromSchema(first, depth+1)
			}
		}
	}
	return leak
}

func copyLeaks(t *testing.T, s *mcp.Server, tool string, args, structured map[string]any) []string {
	t.Helper()
	text := ""
	if structured != nil {
		text = mustJSON(structured)
	}
	parameters, response := s.AnalyticsCopy(tool, args, text, structured)
	var found []string
	leaks(map[string]any{"parameters": parameters, "response": response}, tool, false, &found)
	return found
}

// Every Hack tool's declared input and output schema, filled with a planted
// value, loses each value of a promised class in the telemetry copy.
func TestAnalyticsHackSchemasKeepPersonDataOut(t *testing.T) {
	s := newTestServer(nil)
	tools := Tools()
	if len(tools) < 100 {
		t.Fatalf("only %d Hack tools", len(tools))
	}
	for _, tool := range tools {
		args, _ := sampleFromSchema(tool.InputSchema, 0).(map[string]any)
		structured, _ := sampleFromSchema(tool.OutputSchema, 0).(map[string]any)
		if found := copyLeaks(t, s, tool.Name, args, structured); len(found) > 0 {
			t.Errorf("%s keeps %v", tool.Name, found)
		}
	}
}

// Results recorded from the REST shapes Hack tools pass through
// (products/hack/hackhttp), one or more per person-bearing tool.
func TestAnalyticsHackRecordedResultsKeepPersonDataOut(t *testing.T) {
	s := newTestServer(nil)
	person := map[string]any{"user_id": leak, "email": leak, "display_name": leak, "admin": true}
	for _, tc := range []struct {
		tool       string
		args       map[string]any
		structured map[string]any
	}{
		{"hack_get_my_team_messages", map[string]any{"slug": "spring"}, map[string]any{"response": []any{map[string]any{"id": "m1", "sender_name": leak, "text": "Ready", "from_team": true}}}},
		{"hack_get_team_messages", map[string]any{"slug": "spring", "team": "blue-fox"}, map[string]any{"response": []any{map[string]any{"sender_name": leak, "sender_id": leak}}}},
		{"hack_get_judging_settings", nil, map[string]any{"response": map[string]any{"organiser_judges": []any{leak}, "judges": []any{map[string]any{"user_id": leak, "name": leak, "organiser": true}}}}},
		{"hack_set_judging_settings", map[string]any{"slug": "spring", "body": map[string]any{"organiser_judges": []any{leak}}}, nil},
		{"hack_get_judging_dashboard", nil, map[string]any{"response": map[string]any{"judges": []any{map[string]any{"judge_id": leak, "judge_name": leak}}, "former_judges": []any{map[string]any{"user_id": leak, "display_name": leak, "role": "judge"}}}}},
		{"hack_get_conflicts", nil, map[string]any{"response": []any{map[string]any{"judge_id": leak, "judge_name": leak, "team_id": "t1", "declared_by": leak}}}},
		{"hack_get_people", nil, map[string]any{"response": []any{map[string]any{"user_id": leak, "email": leak, "display_name": leak, "role": "participant", "team": map[string]any{"slug": "blue-fox", "name": "Blue Fox"}}}}},
		{"hack_get_teams", nil, map[string]any{"response": []any{map[string]any{"id": "t1", "slug": "blue-fox", "name": "Blue Fox", "code": leak, "link": leak, "members": []any{person}}}}},
		{"hack_get_team_status", nil, map[string]any{"response": map[string]any{"team": map[string]any{"slug": "blue-fox", "name": "Blue Fox", "code": leak, "link": "https://simple-hack.app/team/" + leak, "members": []any{person}}}}},
		{"hack_list_open_teams", nil, map[string]any{"response": map[string]any{"teams": []any{map[string]any{"name": "Blue Fox", "members": []any{leak}}}, "solo": []any{map[string]any{"user_id": leak, "display_name": leak, "intro": "Designer"}}}}},
		{"hack_fill_teams", nil, map[string]any{"response": map[string]any{"placements": []any{map[string]any{"team": "blue-fox", "name": "Blue Fox", "members": []any{map[string]any{"user_id": leak, "display_name": leak}}}}}}},
		{"hack_preview_leave_event", nil, map[string]any{"team": map[string]any{"slug": "blue-fox", "name": "Blue Fox", "others": []any{leak}}, "organisers_staying": []any{leak}, "successors": []any{map[string]any{"user_id": leak, "name": leak, "email": leak}}}},
		{"hack_preview_application", nil, map[string]any{"response": map[string]any{"organiser_name": leak, "organisation": "Jakarta Makers", "account_name": leak}}},
		{"hack_join_event", map[string]any{"code": leak, "accept_coc": true, "display_name": leak, "answers": map[string]any{"q1": leak}}, map[string]any{"response": map[string]any{"me": map[string]any{"user_id": leak, "display_name": leak}}}},
		{"hack_get_applications", nil, map[string]any{"response": []any{map[string]any{"user_id": leak, "display_name": leak, "city": leak, "country": leak, "age_range": leak, "gender": leak, "answers": []any{map[string]any{"answer": leak}}}}}},
		{"hack_get_entry", nil, map[string]any{"entry": map[string]any{"title": "Lunch Line", "updated_by": leak, "submitted_by": leak}}},
		{"hack_create_organiser_invite", nil, map[string]any{"url": "https://simple-hack.app/organiser/" + leak}},
		{"hack_get_event", nil, map[string]any{"organiser": map[string]any{"name": leak, "contact_email": leak, "join_url": leak, "judge_url": leak, "join_code": leak, "judge_code": leak}, "created_by_email": leak}},
	} {
		if found := copyLeaks(t, s, tc.tool, tc.args, tc.structured); len(found) > 0 {
			t.Errorf("%s keeps %v", tc.tool, found)
		}
	}
	// Team and event names are sent, as the privacy page says.
	_, response := s.AnalyticsCopy("hack_get_teams", nil, `{"response":[{"slug":"blue-fox","name":"Blue Fox","members":[]}]}`, nil)
	if !strings.Contains(mustJSON(response), "Blue Fox") {
		t.Errorf("team name removed: %s", mustJSON(response))
	}
}

// Every JSON key the Hack REST handlers, models and app layer write: a value
// under a key of a promised class, alone or in a person's object, does not
// reach the copy. A new field such as reviewer_name fails here first.
func TestAnalyticsHackRESTKeysKeepPersonDataOut(t *testing.T) {
	keyPattern := regexp.MustCompile(`json:"([a-z0-9_]+)|"([a-z][a-z0-9_]*)":`)
	keys := map[string]bool{}
	for _, dir := range []string{"../hackhttp", "../model", "../app", "../rules"} {
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			body, _ := os.ReadFile(path)
			for _, m := range keyPattern.FindAllStringSubmatch(string(body), -1) {
				keys[m[1]+m[2]] = true
			}
			return nil
		})
	}
	if len(keys) < 200 {
		t.Fatalf("found only %d keys", len(keys))
	}
	names := make([]string, 0, len(keys))
	for key := range keys {
		if promisedClass(key) != "" {
			names = append(names, key)
		}
	}
	sort.Strings(names)
	s := newTestServer(nil)
	for _, key := range names {
		for _, structured := range []map[string]any{
			{key: leak},
			{key: []any{leak}},
			{"members": []any{map[string]any{"user_id": "u", key: leak}}},
		} {
			if found := copyLeaks(t, s, "hack_get_event", nil, structured); len(found) > 0 {
				t.Errorf("key %s (%s) kept: %v", key, promisedClass(key), found)
			}
		}
	}
}

// A refused call never tells AgentCat an unlisted event's stage or that it
// exists; a member, a team website's connection or a listed event get both
// tags, and a team website call names its event by owner.
func TestAnalyticsTagsOnlyForEventsTheCallerMaySee(t *testing.T) {
	facts := map[string]EventFact{
		"draft":  {Found: true, Role: "none", Stage: "draft"},
		"listed": {Found: true, Role: "none", Stage: "open", Listed: true},
		"mine":   {Found: true, Role: "participant", Stage: "building"},
	}
	var asked []string
	tag := AnalyticsTags(func(_ context.Context, slug, account, user string) EventFact {
		asked = append(asked, slug)
		if slug == "" && account == "acct-team" {
			return EventFact{Found: true, Role: "none", Stage: "building", Held: true}
		}
		return facts[slug]
	})
	for _, tc := range []struct {
		caller mcp.Caller
		args   map[string]any
		want   map[string]string
	}{
		{mcp.Caller{UserID: "u"}, map[string]any{"slug": "draft"}, nil},
		{mcp.Caller{UserID: "u"}, map[string]any{"slug": "missing"}, nil},
		{mcp.Caller{UserID: "u"}, map[string]any{"slug": "listed"}, map[string]string{"role": "none", "stage": "open"}},
		{mcp.Caller{UserID: "u"}, map[string]any{"slug": "Mine"}, map[string]string{"role": "participant", "stage": "building"}},
		{mcp.Caller{UserID: "u"}, map[string]any{"owner": "mine", "name": "blue-fox"}, map[string]string{"role": "participant", "stage": "building"}},
		{mcp.Caller{UserID: "u"}, map[string]any{"owner": "draft"}, nil},
		{mcp.Caller{Owner: "acct-team"}, map[string]any{}, map[string]string{"role": "none", "stage": "building"}},
	} {
		got := tag(context.Background(), tc.caller, "hack_get_people", tc.args)
		if mustJSON(got) != mustJSON(tc.want) && !(len(got) == 0 && tc.want == nil) {
			t.Errorf("%v: tags %v, want %v", tc.args, got, tc.want)
		}
	}
	if strings.Join(asked, ",") != "draft,missing,listed,mine,mine,draft," {
		t.Errorf("asked for %v", asked)
	}
}

// An app that froze the older tool list still sends table (and for and
// values) with a team site's storage_sql_execute: the call runs as before,
// though the schema no longer lists them.
func TestHackSiteSQLExecuteAcceptsFormerArguments(t *testing.T) {
	up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){}}
	s := newTestServer(up)
	for _, tool := range listedTools(t, sendMode(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, mcp.CallerModeEvents)) {
		if tool["name"] != "storage_sql_execute" {
			continue
		}
		props := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
		if props["table"] != nil || props["for"] != nil || props["values"] != nil {
			t.Fatalf("former arguments advertised: %v", props)
		}
	}
	text, _, _ := resultOf(t, sendMode(t, s, toolCall("storage_sql_execute", map[string]any{
		"owner": "spring", "site": "blue-fox", "name": "data", "sql": "INSERT INTO rows (a) VALUES (1)", "table": "rows",
	}), mcp.CallerModeEvents))
	if strings.Contains(text, "unexpected argument") || up.len() == 0 {
		t.Fatalf("refused before dispatch: %s", text)
	}
}

// A field nobody has classified, such as a judge's private note, never
// reaches AgentCat: a Simple Hack copy keeps a string only under a key
// allowed in host/mcp/analytics_allow.go. If this fails for a field you
// added, do not allow its key unless its values come from a fixed set or
// name a thing, never what a person typed.
func TestAnalyticsHackUnknownTextNeverLeaves(t *testing.T) {
	const typed = "TYPED_BY_A_PERSON"
	s := newTestServer(nil)
	unknown := func() map[string]any {
		return map[string]any{"note": typed, "anything_new": typed, "list_of_new": []any{typed},
			"body": map[string]any{"anything_new": typed}, "items": []any{map[string]any{"note": typed}}}
	}
	for _, tool := range Tools() {
		args, _ := sampleFromSchema(tool.InputSchema, 0).(map[string]any)
		if args == nil {
			args = map[string]any{}
		}
		for key, value := range unknown() {
			args[key] = value
		}
		structured := unknown()
		for _, text := range []string{mustJSON(structured), "Saved. Note: " + typed} {
			parameters, response := s.AnalyticsCopy(tool.Name, args, text, structured)
			if copied := mustJSON([]any{parameters, response}); strings.Contains(copied, typed) {
				t.Errorf("%s sends an unclassified field: %s", tool.Name, copied)
			}
		}
	}
	// Allowed keys still go: the copy stays useful.
	parameters, _ := s.AnalyticsCopy("hack_score_team", map[string]any{"slug": "spring", "team": "blue-fox", "note": typed}, "", nil)
	if copied := mustJSON(parameters); !strings.Contains(copied, "spring") || !strings.Contains(copied, "blue-fox") {
		t.Errorf("allowed keys removed: %s", copied)
	}
}
