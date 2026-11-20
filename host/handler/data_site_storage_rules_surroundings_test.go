package handler

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestRulesVisitorRightsList(t *testing.T) {
	s := newStorageTestSite(t)
	s.must(t, "PUT", "/resources/data", `{"kind":"sqlite","rules":{"policies":[{"command":"read","using":"true"},{"command":"add","check":"auth.signed_in"},{"command":"edit","using":"auth.role == \"owner\""},{"command":"delete","using":"auth.role == \"owner\""}]},"tables":{"closed":{"rules":{"policies":[]}}}}`, s.key, 201)
	s.must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE entries(body TEXT)"}, s.key, 200)
	s.must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE closed(secret TEXT)"}, s.key, 200)
	// Exercise discovery with each resolved browser identity as well as the MCP
	// visitor pass in TestRulesVisitorMCP below.
	for _, who := range []string{"anon", "alice", "owner-site"} {
		r := httptest.NewRequest("GET", "https://"+s.host+"/", nil)
		r.SetPathValue("sitename", "shop")
		for k, v := range s.callers[who] {
			r.Header.Set(k, v)
		}
		out, err := s.a.sites.visitorRead(r, s.sid, s.ownerID, "shop", map[string]any{})
		if err != nil {
			t.Fatalf("%s: %v", who, err)
		}
		list := out["resources"].([]map[string]any)
		if len(list) != 1 || list[0]["table"] != "entries" {
			t.Fatalf("%s: %v", who, list)
		}
		if _, ok := list[0]["preset"]; ok {
			t.Fatal("rules entry exposes preset fill")
		}
		want := map[string]any{"read": true, "add": who != "anon", "edit_own": who == "owner-site", "delete_own": who == "owner-site", "read_own_only": false}
		if !reflect.DeepEqual(list[0]["rights"], want) {
			t.Fatalf("%s: got %v want %v", who, list[0]["rights"], want)
		}
	}
}

func TestRulesVisitorMCP(t *testing.T) {
	f := newVisitorFixture(t)
	f.must(t, "PUT", "/resources/data", `{"kind":"sqlite","rules":{"policies":[{"command":"read","using":"visitor_id == auth.id"},{"command":"add","check":"auth.signed_in"},{"command":"edit","using":"auth.signed_in"}]}}`, f.key, 201)
	f.must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE entries(body TEXT)"}, f.key, 200)
	f.must(t, "POST", "/sqlite/data/tables/entries/rows", `{"body":"alice-hidden"}`, f.callers["alice"], 200)
	args := f.args("sqlite", "data")
	args["table"] = "entries"
	args["value"] = map[string]any{"body": "bob-visible"}
	f.call(t, "visitor_add_entry", args, "")
	out := f.call(t, "visitor_read_site", map[string]any{"address": "https://" + f.host}, "")
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "alice-hidden") || !strings.Contains(string(raw), "bob-visible") || strings.Contains(string(raw), `"preset"`) {
		t.Fatalf("discovery: %s", raw)
	}
	if !strings.Contains(string(raw), `"edit_own":true`) {
		t.Fatalf("rights: %s", raw)
	}
}

func TestRulesOwnerOnSiteForRecordsHTTP(t *testing.T) {
	s := newStorageTestSite(t)
	s.must(t, "PUT", "/resources/records", storageRules("sqlite", rulesRecords), s.key, 201)
	s.must(t, "POST", "/sqlite/records/schema", map[string]string{"sql": "CREATE TABLE entries(body TEXT)"}, s.key, 200)
	path := "/sqlite/records/tables/entries/rows"
	added := s.must(t, "POST", path+"?for="+s.aliceID, `{"body":"from owner"}`, s.callers["owner-site"], 200)
	id := fmt.Sprint(added.json(t)["last_insert_id"])
	got := s.must(t, "GET", path+"/"+id, nil, s.callers["alice"], 200).json(t)
	cols, row := got["columns"].([]any), got["row"].([]any)
	if row[colIndex(cols, "visitor_id")] != s.aliceID || row[colIndex(cols, "written_by")] != "owner" {
		t.Fatal(got)
	}
	s.must(t, "GET", path+"/"+id, nil, s.callers["bob"], 404)
}

func TestRulesSpamCheckHook(t *testing.T) {
	s, fake := commentsSite(t)
	// Only the table override has a checked add; default rules do not.
	s.must(t, "PUT", "/resources/guestbook", `{"kind":"sqlite","rules":{"policies":[{"command":"read","using":"true"}]},"tables":{"entries":{"rules":{"policies":[{"command":"read","using":"true"},{"command":"add","check":"auth.checked"}]}}}}`, s.key, 200)
	path := "/sqlite/guestbook/tables/entries/rows"
	r := s.must(t, "POST", path, `{"message":"unchecked"}`, s.callers["anon"], 403)
	if r.json(t)["code"] != "spam_check_required" || fake.calls() != 0 {
		t.Fatalf("unchecked: %s calls=%d", r.body, fake.calls())
	}
	s.must(t, "POST", path, `{"message":"checked"}`, withToken(s.callers["anon"], s.token("guestbook")), 200)
	if fake.calls() != 1 {
		t.Fatalf("spam check calls=%d", fake.calls())
	}
	// Mentioning the literal text is not a reference to the binding.
	s.must(t, "PUT", "/resources/guestbook", `{"kind":"sqlite","rules":{"policies":[{"command":"read","using":"true"},{"command":"add","check":"message == \"auth.checked\""}]},"tables":{}}`, s.key, 200)
	s.must(t, "POST", path, `{"message":"auth.checked"}`, withToken(s.callers["anon"], s.token("guestbook")), 200)
	if fake.calls() != 1 {
		t.Fatalf("literal triggered spam check: %d", fake.calls())
	}
}
