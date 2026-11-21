package datatest

import (
	"encoding/json"
	"fmt"
	"github.com/vsriram/simple-host/host/handler"
	"github.com/vsriram/simple-host/host/server/servertest"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestRulesVisitorRightsList(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	s.Must(t, "PUT", "/resources/data", `{"kind":"sqlite","rules":{"policies":[{"command":"read","using":"true"},{"command":"add","check":"auth.signed_in"},{"command":"edit","using":"auth.role == \"owner\""},{"command":"delete","using":"auth.role == \"owner\""}]},"tables":{"closed":{"rules":{"policies":[]}}}}`, s.Key, 201)
	s.Must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE entries(body TEXT)"}, s.Key, 200)
	s.Must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE closed(secret TEXT)"}, s.Key, 200)
	// Exercise discovery with each resolved browser identity as well as the MCP
	// visitor pass in TestRulesVisitorMCP below.
	for _, who := range []string{"anon", "alice", "owner-site"} {
		r := httptest.NewRequest("GET", "https://"+s.Host+"/", nil)
		r.SetPathValue("sitename", "shop")
		for k, v := range s.Callers[who] {
			r.Header.Set(k, v)
		}
		out, err := handler.VisitorTools{Access: s.A.Access, Data: s.A.Data}.Read(r, s.Sid, s.OwnerID, "shop", map[string]any{})
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
	f := servertest.NewVisitorFixture(t)
	f.Must(t, "PUT", "/resources/data", `{"kind":"sqlite","rules":{"policies":[{"command":"read","using":"visitor_id == auth.id"},{"command":"add","check":"auth.signed_in"},{"command":"edit","using":"auth.signed_in"}]}}`, f.Key, 201)
	f.Must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE entries(body TEXT)"}, f.Key, 200)
	f.Must(t, "POST", "/sqlite/data/tables/entries/rows", `{"body":"alice-hidden"}`, f.Callers["alice"], 200)
	args := f.Args("sqlite", "data")
	args["table"] = "entries"
	args["value"] = map[string]any{"body": "bob-visible"}
	f.Call(t, "visitor_add_entry", args, "")
	out := f.Call(t, "visitor_read_site", map[string]any{"address": "https://" + f.Host}, "")
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "alice-hidden") || !strings.Contains(string(raw), "bob-visible") || strings.Contains(string(raw), `"preset"`) {
		t.Fatalf("discovery: %s", raw)
	}
	if !strings.Contains(string(raw), `"edit_own":true`) {
		t.Fatalf("rights: %s", raw)
	}
}

func TestRulesOwnerOnSiteForRecordsHTTP(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	s.Must(t, "PUT", "/resources/records", servertest.StorageRules("sqlite", servertest.RulesRecords), s.Key, 201)
	s.Must(t, "POST", "/sqlite/records/schema", map[string]string{"sql": "CREATE TABLE entries(body TEXT)"}, s.Key, 200)
	path := "/sqlite/records/tables/entries/rows"
	added := s.Must(t, "POST", path+"?for="+s.AliceID, `{"body":"from owner"}`, s.Callers["owner-site"], 200)
	id := fmt.Sprint(added.JSON(t)["last_insert_id"])
	got := s.Must(t, "GET", path+"/"+id, nil, s.Callers["alice"], 200).JSON(t)
	cols, row := got["columns"].([]any), got["row"].([]any)
	if row[colIndex(cols, "visitor_id")] != s.AliceID || row[colIndex(cols, "written_by")] != "owner" {
		t.Fatal(got)
	}
	s.Must(t, "GET", path+"/"+id, nil, s.Callers["bob"], 404)
}

func TestRulesSpamCheckHook(t *testing.T) {
	s, fake := commentsSite(t)
	// Only the table override has a checked add; default rules do not.
	s.Must(t, "PUT", "/resources/guestbook", `{"kind":"sqlite","rules":{"policies":[{"command":"read","using":"true"}]},"tables":{"entries":{"rules":{"policies":[{"command":"read","using":"true"},{"command":"add","check":"auth.checked"}]}}}}`, s.Key, 200)
	path := "/sqlite/guestbook/tables/entries/rows"
	r := s.Must(t, "POST", path, `{"message":"unchecked"}`, s.Callers["anon"], 403)
	if r.JSON(t)["code"] != "spam_check_required" || fake.calls() != 0 {
		t.Fatalf("unchecked: %s calls=%d", r.Body, fake.calls())
	}
	s.Must(t, "POST", path, `{"message":"checked"}`, withToken(s.Callers["anon"], s.Token("guestbook")), 200)
	if fake.calls() != 1 {
		t.Fatalf("spam check calls=%d", fake.calls())
	}
	// Mentioning the literal text is not a reference to the binding.
	s.Must(t, "PUT", "/resources/guestbook", `{"kind":"sqlite","rules":{"policies":[{"command":"read","using":"true"},{"command":"add","check":"message == \"auth.checked\""}]},"tables":{}}`, s.Key, 200)
	s.Must(t, "POST", path, `{"message":"auth.checked"}`, withToken(s.Callers["anon"], s.Token("guestbook")), 200)
	if fake.calls() != 1 {
		t.Fatalf("literal triggered spam check: %d", fake.calls())
	}
}
