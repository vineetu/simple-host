package datatest

import (
	"net/http/httptest"
	"testing"

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/data/datahttp"
	"github.com/vsriram/simple-host/host/server/servertest"
)

func TestStorageRulesReadTableOverride(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	s.Must(t, "PUT", "/resources/data", `{"kind":"sqlite","rules":{"policies":[{"command":"read","using":"true"}]},"tables":{"closed":{"rules":{"policies":[]}}}}`, s.Key, 201)
	for _, table := range []string{"open", "closed"} {
		s.Must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE " + table + "(body TEXT)"}, s.Key, 200)
		s.Must(t, "POST", "/sqlite/data/execute", map[string]string{"sql": "INSERT INTO " + table + " VALUES('secret')"}, s.Key, 200)
		for _, suffix := range []string{"", "/1"} {
			status := 200
			if table == "closed" {
				status = 403
			}
			r := s.Must(t, "GET", "/sqlite/data/tables/"+table+"/rows"+suffix, nil, s.Callers["anon"], status)
			if table == "closed" && r.JSON(t)["code"] != "forbidden" {
				t.Fatal(string(r.Body))
			}
		}
	}
}

func TestStorageRulesReadAuthenticationResponses(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	s.Must(t, "PUT", "/resources/data", servertest.StorageRules("sqlite", servertest.RulesBoard), s.Key, 201)
	s.Must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE entries(body TEXT)"}, s.Key, 200)
	expected := map[string]servertest.Resp{}
	for _, suffix := range []string{"", "/1"} {
		expected["anon"+suffix] = s.Must(t, "GET", "/sqlite/data/tables/entries/rows"+suffix, nil, s.Callers["anon"], 401)
	}
	if _, err := s.A.Database.Exec(`UPDATE users SET suspended_at=now() WHERE id=$1`, s.AliceID); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "/1"} {
		// A suspended account has no visitor session (accounts.VisitorSession).
		expected["alice"+suffix] = s.Must(t, "GET", "/sqlite/data/tables/entries/rows"+suffix, nil, s.Callers["alice"], 401)
	}
	for _, who := range []string{"anon", "alice"} {
		for _, suffix := range []string{"", "/1"} {
			want := expected[who+suffix]
			got := s.Must(t, "GET", "/sqlite/data/tables/entries/rows"+suffix, nil, s.Callers[who], want.Status)
			if string(got.Body) != string(want.Body) {
				t.Fatalf("%s: %s != %s", who, got.Body, want.Body)
			}
			// Execution-stage failures use the same HTTP serializer as authorization.
			code, message := "sign_in_required", "sign in to this site first"
			w := httptest.NewRecorder()
			datahttp.WriteStorageError(w, &appstorage.Error{Code: code, Message: message}, 400)
			if w.Code != want.Status || w.Body.String() != string(want.Body) {
				t.Fatalf("execution: %d %s; want %d %s", w.Code, w.Body.String(), want.Status, want.Body)
			}
		}
	}
}
