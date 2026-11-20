package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	appstorage "github.com/vsriram/simple-host/host/data"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

type replayBindings struct {
	appstorage.NoBindings
	member, deadline bool
}

func (*replayBindings) OwnerOnSite(context.Context, string, string) (bool, error) { return false, nil }
func (*replayBindings) Schema() ruleengine.BindingSchema {
	return ruleengine.BindingSchema{"event.member": {Type: ruleengine.TypeBool}, "event.before_deadline": {Type: ruleengine.TypeBool}}
}
func (p *replayBindings) Values(context.Context, appstorage.BindingCall, []string) (ruleengine.Values, error) {
	return ruleengine.Values{"event.member": ruleengine.Bool(p.member), "event.before_deadline": ruleengine.Bool(p.deadline)}, nil
}

func saveWriteHTTPRules(t *testing.T, s storageTestSite, add, check string, status int) {
	t.Helper()
	policies := []ruleengine.Policy{{Command: ruleengine.CmdRead, Using: "true"}, {Command: ruleengine.CmdAdd, Check: add}, {Command: ruleengine.CmdEdit, Using: "auth.signed_in", Check: check}, {Command: ruleengine.CmdDelete, Using: "auth.signed_in"}}
	s.must(t, "PUT", "/resources/data", map[string]any{"kind": "sqlite", "rules": json.RawMessage(ruleengine.EncodeSet(ruleengine.PolicySet{Policies: policies}))}, s.key, status)
}

func TestStorageRulesWriteReplay(t *testing.T) {
	s := newStorageTestSite(t)
	p := &replayBindings{member: true, deadline: true}
	s.a.sites.SetStorageBindings(p)
	saveWriteHTTPRules(t, s, "auth.signed_in", "true", 201)
	s.must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE entries(status TEXT)"}, s.key, 200)
	route := "/sqlite/data/tables/entries/rows"
	for _, change := range []string{"unchanged", "rule", "suspended", "membership", "deadline", "row-dependent"} {
		t.Run(change, func(t *testing.T) {
			p.member, p.deadline = true, true
			add := "auth.signed_in"
			if change == "membership" {
				add += " && event.member"
			}
			if change == "deadline" {
				add += " && event.before_deadline"
			}
			saveWriteHTTPRules(t, s, add, "true", 200)
			h := map[string]string{}
			for k, v := range s.callers["alice"] {
				h[k] = v
			}
			h["Idempotency-Key"] = "rules-" + change
			first := s.must(t, "POST", route, `{"status":"draft"}`, h, 200)
			want, code := 200, ""
			switch change {
			case "rule":
				saveWriteHTTPRules(t, s, `auth.role == "owner"`, "true", 200)
				want, code = 403, "forbidden"
			case "suspended":
				if _, err := s.a.database.Exec(`UPDATE users SET suspended_at=now() WHERE id=$1`, s.aliceID); err != nil {
					t.Fatal(err)
				}
				want, code = 401, "sign_in_required"
			case "membership":
				p.member = false
				want, code = 403, "forbidden"
			case "deadline":
				p.deadline = false
				want, code = 403, "forbidden"
			case "row-dependent":
				saveWriteHTTPRules(t, s, `auth.signed_in && status == "approved"`, "true", 200)
			}
			replay := s.must(t, "POST", route, `{"status":"draft"}`, h, want)
			if want == 200 {
				if string(replay.body) != string(first.body) {
					t.Fatalf("not stored replay: %s / %s, headers=%v", first.body, replay.body, replay.header)
				}
			} else if replay.json(t)["code"] != code || replay.header.Get("Idempotent-Replayed") != "" {
				t.Fatalf("replay refusal: %s", replay.body)
			}
			if change == "row-dependent" {
				h["Idempotency-Key"] += "-new"
				r := s.must(t, "POST", route, `{"status":"draft"}`, h, 403)
				if r.json(t)["code"] != "forbidden" {
					t.Fatal(string(r.body))
				}
			}
			// One initial write per subtest; neither replay nor failed fresh key writes.
			count := s.must(t, "POST", "/sqlite/data/query", map[string]string{"sql": "SELECT count(*) FROM entries"}, s.key, 200).json(t)
			rows := count["rows"].([]any)
			n := rows[0].([]any)[0].(float64)
			expected := map[string]float64{"unchanged": 1, "rule": 2, "suspended": 3, "membership": 4, "deadline": 5, "row-dependent": 6}[change]
			if n != expected {
				t.Fatalf("rows=%v want=%v", n, expected)
			}
			if change == "suspended" {
				if _, err := s.a.database.Exec(`UPDATE users SET suspended_at=NULL WHERE id=$1`, s.aliceID); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestStorageRulesWriteFailedCheckMatchesMissing(t *testing.T) {
	s := newStorageTestSite(t)
	saveWriteHTTPRules(t, s, "auth.signed_in", `status == "approved"`, 201)
	s.must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE entries(status TEXT)"}, s.key, 200)
	route := "/sqlite/data/tables/entries/rows"
	s.must(t, "POST", route, `{"status":"draft"}`, s.callers["alice"], 200)
	failed := s.must(t, "PATCH", route+"/1", `{"status":"rejected"}`, s.callers["alice"], 404)
	missing := s.must(t, "PATCH", route+"/99", `{"status":"rejected"}`, s.callers["alice"], 404)
	if string(failed.body) != string(missing.body) {
		t.Fatalf("failed check %s != missing %s", failed.body, missing.body)
	}
	row := s.must(t, "GET", route+"/1", nil, s.callers["alice"], 200).json(t)
	if row["row"].([]any)[0] != "draft" {
		t.Fatal(row)
	}
}

func TestStorageRulesWriteServerColumnSpelling(t *testing.T) {
	s := newStorageTestSite(t)
	saveWriteHTTPRules(t, s, "auth.signed_in", "true", 201)
	s.must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE entries(body TEXT, visitor_id TEXT, created_at TEXT, updated_at TEXT)"}, s.key, 200)
	route := "/sqlite/data/tables/entries/rows"
	count := 0
	for _, name := range []string{"visitor_id", "written_by", "created_at", "updated_at"} {
		for _, supplied := range []string{name, strings.ToUpper(name)} {
			s.must(t, "POST", route, map[string]string{"body": "new", supplied: "forged"}, s.callers["alice"], 200)
			count++
		}
	}
	rows := s.must(t, "POST", "/sqlite/data/query", map[string]string{"sql": "SELECT visitor_id, created_at, updated_at FROM entries"}, s.key, 200).json(t)["rows"].([]any)
	if len(rows) != count {
		t.Fatalf("rows=%d want=%d; refused inserts must write nothing", len(rows), count)
	}
	for _, value := range rows {
		row := value.([]any)
		if row[0] != s.aliceID || row[2] != nil {
			t.Fatalf("forged server values survived: %v", row)
		}
		stamp, ok := row[1].(string)
		if _, err := time.Parse(time.RFC3339Nano, stamp); !ok || err != nil {
			t.Fatalf("invalid creation timestamp: %v", row)
		}
	}
}
