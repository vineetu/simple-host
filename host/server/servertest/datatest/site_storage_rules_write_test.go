package datatest

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
	member bool
}

func (*replayBindings) OwnerOnSite(context.Context, string, string) (bool, error) { return false, nil }
func (*replayBindings) Schema() ruleengine.BindingSchema {
	return ruleengine.BindingSchema{"event.member": {Type: ruleengine.TypeBool}}
}
func (p *replayBindings) Values(context.Context, appstorage.BindingCall, []string) (ruleengine.Values, error) {
	return ruleengine.Values{"event.member": ruleengine.Bool(p.member)}, nil
}

func saveWriteRules(t *testing.T, s Shop, add, check string, status int) {
	t.Helper()
	policies := []ruleengine.Policy{{Command: ruleengine.CmdRead, Using: "true"}, {Command: ruleengine.CmdAdd, Check: add}, {Command: ruleengine.CmdEdit, Using: "auth.signed_in", Check: check}, {Command: ruleengine.CmdDelete, Using: "auth.signed_in"}}
	s.Must(t, "PUT", "/resources/data", map[string]any{"kind": "sqlite", "rules": json.RawMessage(ruleengine.EncodeSet(ruleengine.PolicySet{Policies: policies}))}, s.Key, status)
}

// An idempotent replay rechecks today's rules: a write the rules (or a
// product binding) no longer allow is refused, never replayed.
func TestStorageRulesWriteReplay(t *testing.T) {
	s := NewShop(t)
	p := &replayBindings{member: true}
	s.A.Data.SetStorageBindings(p)
	saveWriteRules(t, s, "auth.signed_in", "true", 201)
	s.Must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE entries(status TEXT)"}, s.Key, 200)
	route := "/sqlite/data/tables/entries/rows"
	for i, change := range []string{"unchanged", "rule", "membership", "row-dependent"} {
		t.Run(change, func(t *testing.T) {
			p.member = true
			add := "auth.signed_in"
			if change == "membership" {
				add += " && event.member"
			}
			saveWriteRules(t, s, add, "true", 200)
			h := rtCopy(s.Callers["alice"], "Idempotency-Key", "rules-"+change)
			first := s.Must(t, "POST", route, `{"status":"draft"}`, h, 200)
			want := 200
			switch change {
			case "rule":
				saveWriteRules(t, s, `auth.role == "owner"`, "true", 200)
				want = 403
			case "membership":
				p.member = false
				want = 403
			case "row-dependent":
				saveWriteRules(t, s, `auth.signed_in && status == "approved"`, "true", 200)
			}
			replay := s.Must(t, "POST", route, `{"status":"draft"}`, h, want)
			if want == 200 && string(replay.Body) != string(first.Body) {
				t.Fatalf("not a stored replay: %s / %s", first.Body, replay.Body)
			}
			if want != 200 && (replay.Code() != "forbidden" || replay.Header.Get("Idempotent-Replayed") != "") {
				t.Fatalf("replay refusal: %s", replay.Body)
			}
			if change == "row-dependent" {
				s.Must(t, "POST", route, `{"status":"draft"}`, rtCopy(h, "Idempotency-Key", "fresh"), 403)
			}
			n := s.Must(t, "POST", "/sqlite/data/query", map[string]string{"sql": "SELECT count(*) FROM entries"}, s.Key, 200).JSON(t)["rows"].([]any)[0].([]any)[0].(float64)
			if n != float64(i+1) {
				t.Fatalf("rows=%v want=%d", n, i+1)
			}
		})
	}
}

// rtCopy is h with one header changed.
func rtCopy(h map[string]string, k, v string) map[string]string {
	out := map[string]string{k: v}
	for hk, hv := range h {
		if hk != k {
			out[hk] = hv
		}
	}
	return out
}

// An edit whose check fails answers like a missing row and changes nothing.
func TestStorageRulesWriteFailedCheckMatchesMissing(t *testing.T) {
	s := NewShop(t)
	saveWriteRules(t, s, "auth.signed_in", `status == "approved"`, 201)
	s.Must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE entries(status TEXT)"}, s.Key, 200)
	route := "/sqlite/data/tables/entries/rows"
	s.Must(t, "POST", route, `{"status":"draft"}`, s.Callers["alice"], 200)
	failed := s.Must(t, "PATCH", route+"/1", `{"status":"rejected"}`, s.Callers["alice"], 404)
	missing := s.Must(t, "PATCH", route+"/99", `{"status":"rejected"}`, s.Callers["alice"], 404)
	if string(failed.Body) != string(missing.Body) {
		t.Fatalf("failed check %s != missing %s", failed.Body, missing.Body)
	}
	if row := s.Must(t, "GET", route+"/1", nil, s.Callers["alice"], 200).JSON(t); row["row"].([]any)[0] != "draft" {
		t.Fatal(row)
	}
}

// A visitor's values for the server's columns, in any spelling, are ignored.
func TestStorageRulesWriteServerColumnSpelling(t *testing.T) {
	s := NewShop(t)
	saveWriteRules(t, s, "auth.signed_in", "true", 201)
	s.Must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE entries(body TEXT, visitor_id TEXT, created_at TEXT, updated_at TEXT)"}, s.Key, 200)
	route := "/sqlite/data/tables/entries/rows"
	for _, name := range []string{"visitor_id", "WRITTEN_BY", "created_at", "UPDATED_AT"} {
		s.Must(t, "POST", route, map[string]string{"body": "new", name: "forged"}, s.Callers["alice"], 200)
	}
	rows := s.Must(t, "POST", "/sqlite/data/query", map[string]string{"sql": "SELECT visitor_id, created_at, updated_at FROM entries"}, s.Key, 200).JSON(t)["rows"].([]any)
	for _, value := range rows {
		row := value.([]any)
		stamp, _ := row[1].(string)
		if _, err := time.Parse(time.RFC3339Nano, stamp); row[0] != s.AliceID || row[2] != nil || err != nil || strings.Contains(stamp, "forged") {
			t.Fatalf("forged server values survived: %v", row)
		}
	}
}
