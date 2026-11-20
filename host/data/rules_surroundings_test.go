package data

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

func TestRulesParentReferences(t *testing.T) {
	for _, target := range []string{"parent(a,b)", "parent"} {
		t.Run(target, func(t *testing.T) {
			s, c, in := readFixture(t, fmt.Sprintf(`CREATE TABLE parent(a TEXT, b INTEGER, visitor_id TEXT, PRIMARY KEY(a,b)); CREATE TABLE entries(id INTEGER PRIMARY KEY, a TEXT, b INTEGER, body TEXT, FOREIGN KEY(a,b) REFERENCES %s);`, target), writeSet("true", "true", "auth.signed_in", "true"))
			// The child inherits open reads; the parent override limits visibility.
			tables := map[string]ResourceTableInput{"parent": {Rules: readSet(`visitor_id == auth.id || auth.role == "owner"`)}}
			in.Tables = &tables
			c.Resource = putRules(t, s, in)
			c.VisitorID, c.ActiveChecked = "caller", true
			execReadFixture(t, s, `INSERT INTO parent VALUES('same',1,'caller'),('same',2,'other');`)
			added := mustRuleWrite(t, s, c, "add", 0, `{"a":"same","b":1,"body":"visible"}`, "")
			id := added.Body["last_insert_id"].(int64)
			for _, action := range []string{"add", "edit"} {
				before := ruleDBBytes(t, s)
				mustRuleWrite(t, s, c, action, id, `{"a":"same","b":2,"body":"hidden"}`, "invalid_reference")
				if !bytes.Equal(before, ruleDBBytes(t, s)) {
					t.Fatalf("%s with hidden parent changed database", action)
				}
			}
			// Optional references and partial composite NULLs keep their old semantics.
			mustRuleWrite(t, s, c, "add", 0, `{"a":null,"b":null}`, "")
			mustRuleWrite(t, s, c, "add", 0, `{"a":"same","b":null}`, "invalid_reference")
			c.VisitorID, c.OwnerOnSite = c.OwnerID, true
			mustRuleWrite(t, s, c, "add", 0, `{"a":"same","b":2}`, "")
		})
	}
}

func TestRulesForReadShapes(t *testing.T) {
	raw := json.RawMessage(ruleengine.EncodeSet(ruleengine.PolicySet{Version: 1, Policies: []ruleengine.Policy{
		{Name: "read", Command: ruleengine.CmdRead, Using: `visitor_id == auth.id || auth.role == "owner"`},
		{Name: "add", Command: ruleengine.CmdAdd, Check: `auth.signed_in`},
		{Name: "edit", Command: ruleengine.CmdEdit, Using: `auth.role == "owner"`},
		{Name: "delete", Command: ruleengine.CmdDelete, Using: `auth.role == "owner"`},
	}}))
	for _, tc := range []struct {
		name  string
		rules *json.RawMessage
		code  string
	}{
		{"single", saveSet(ruleengine.Policy{Command: ruleengine.CmdRead, Using: `visitor_id == auth.id || auth.role == "owner"`}, ruleengine.Policy{Command: ruleengine.CmdAdd, Check: `auth.role == "owner"`}), ""},
		{"two", saveSet(ruleengine.Policy{Command: ruleengine.CmdRead, Using: `visitor_id == auth.id`}, ruleengine.Policy{Command: ruleengine.CmdRead, Using: `auth.role == "owner"`}, ruleengine.Policy{Command: ruleengine.CmdAdd, Check: `auth.role == "owner"`}), ""},
		{"records", &raw, ""},
		{"signed_in", saveSet(ruleengine.Policy{Command: ruleengine.CmdRead, Using: `auth.signed_in`}, ruleengine.Policy{Command: ruleengine.CmdAdd, Check: `auth.role == "owner"`}), "for_requires_own"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c, _ := readFixture(t, `CREATE TABLE entries(id INTEGER PRIMARY KEY, body TEXT);`, tc.rules)
			for _, ownerTools := range []bool{false, true} {
				c.Owner, c.OwnerOnSite, c.VisitorID, c.ActiveChecked, c.OwnerFor = ownerTools, !ownerTools, c.OwnerID, true, "target"
				out := mustRuleWrite(t, s, c, "add", 0, `{"body":"for visitor"}`, tc.code)
				if tc.code != "" {
					continue
				}
				owner := c
				owner.Owner = true
				owner.OwnerFor = ""
				row := readGet(t, s, owner, "entries", out.Body["last_insert_id"].(int64), "").Body
				values := map[string]any{}
				for i, col := range row["columns"].([]string) {
					values[col] = row["row"].([]any)[i]
				}
				if values["visitor_id"] != "target" || values["written_by"] != "owner" {
					t.Fatal(values)
				}
			}
		})
	}
}

func TestRulesForMixedCaseAuthorColumns(t *testing.T) {
	for _, writtenBy := range []string{"", "Written_By"} {
		for _, ownerTools := range []bool{false, true} {
			t.Run(fmt.Sprintf("written_by=%s/owner_tools=%t", writtenBy, ownerTools), func(t *testing.T) {
				schema := `CREATE TABLE entries(body TEXT, VISITOR_ID TEXT`
				if writtenBy != "" {
					schema += `, Written_By TEXT NOT NULL DEFAULT ''`
				}
				s, c, _ := readFixture(t, schema+`);`, saveSet(
					ruleengine.Policy{Command: ruleengine.CmdRead, Using: `visitor_id == auth.id || auth.role == "owner"`},
					ruleengine.Policy{Command: ruleengine.CmdAdd, Check: `auth.role == "owner"`},
				))
				c.Owner, c.OwnerOnSite, c.VisitorID, c.ActiveChecked, c.OwnerFor = ownerTools, !ownerTools, c.OwnerID, true, "target"
				out := mustRuleWrite(t, s, c, "add", 0, `{"body":"for visitor"}`, "")
				c.Owner, c.OwnerFor = true, ""
				row := readGet(t, s, c, "entries", out.Body["last_insert_id"].(int64), "").Body
				values := map[string]any{}
				for i, col := range row["columns"].([]string) {
					values[col] = row["row"].([]any)[i]
				}
				column := writtenBy
				if column == "" {
					column = "written_by"
				}
				if values["VISITOR_ID"] != "target" || values[column] != "owner" {
					t.Fatal(values)
				}
			})
		}
	}
}

func TestRulesRightsForCaller(t *testing.T) {
	s, c, _ := readFixture(t, `CREATE TABLE entries(body TEXT);`, writeSet("true", "auth.signed_in", `auth.role == "owner"`, "true"))
	for _, who := range []string{"anonymous", "signed_in", "owner_on_site"} {
		c.VisitorID, c.OwnerOnSite, c.ActiveChecked = "", false, true
		if who == "signed_in" {
			c.VisitorID = "caller"
		}
		if who == "owner_on_site" {
			c.VisitorID, c.OwnerOnSite = c.OwnerID, true
		}
		got, err := s.RuleRights(context.Background(), c, "entries")
		want := map[string]any{"read": true, "add": who != "anonymous", "edit_own": who == "owner_on_site", "delete_own": who == "owner_on_site", "read_own_only": false}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %v %v, want %v", who, got, err, want)
		}
	}
	// A row-dependent right remains listed (Partial is Unknown).
	s, c, _ = readFixture(t, `CREATE TABLE entries(body TEXT);`, readSet(`body == "visible"`))
	got, err := s.RuleRights(context.Background(), c, "entries")
	if err != nil || got["read"] != true {
		t.Fatalf("row-dependent right: %v %v", got, err)
	}
}

func TestRulesPeopleAuthorTracking(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rules   *json.RawMessage
		tracked bool
	}{
		{"ordinary_add", saveSet(ruleengine.Policy{Command: ruleengine.CmdAdd, Check: "true"}), true},
		{"identity_read", readSet(`visitor_id == auth.id`), true},
		{"owner_only", readSet(`auth.role == "owner"`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			column := "visitor_id"
			if tc.name == "identity_read" {
				column = "VISITOR_ID"
			}
			s, c, _ := readFixture(t, "CREATE TABLE entries(body TEXT,"+column+" TEXT);", tc.rules)
			execReadFixture(t, s, fmt.Sprintf(`INSERT INTO entries VALUES('data','%s');`, c.OwnerID))
			if _, err := s.db.Exec(`INSERT INTO site_visitor_signins(site_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, c.SiteID, c.OwnerID); err != nil {
				t.Fatal(err)
			}
			s.deps.VisitorEmail = func(context.Context, string) (string, error) { return "visitor@example.com", nil }
			for _, ownerTools := range []bool{true, false} {
				c.Owner, c.OwnerOnSite = ownerTools, !ownerTools
				people, err := s.SitePeople(context.Background(), c)
				if err != nil || (len(people) == 1) != tc.tracked {
					t.Fatalf("people=%v err=%v", people, err)
				}
				if tc.tracked && (people[0].VisitorID != c.OwnerID || !people[0].HasData) {
					t.Fatal(people)
				}
			}
			c.Owner, c.OwnerOnSite = false, false
			if _, err := s.SitePeople(context.Background(), c); err == nil || err.(*Error).Code != "forbidden" {
				t.Fatalf("visitor people: %v", err)
			}
			if _, err := s.Visitors(context.Background(), VisitorsInput{Call: c, IDs: []string{c.OwnerID}}); err == nil || err.(*Error).Code != "forbidden" {
				t.Fatalf("visitor emails: %v", err)
			}
		})
	}
}
