package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	appstorage "github.com/vsriram/simple-host/host/app/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

func TestStorageRulesInvalidHTTPResponses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(http.ResponseWriter, *appstorage.Error)
	}{
		{"table", writeTableError},
		{"legacy_sql", writeLegacySQLError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.write(w, &appstorage.Error{Code: "rules_invalid", Message: "stored rules are invalid"})
			checkAPIError(t, w, http.StatusForbidden, "rules_invalid")
		})
	}
}

func TestStorageRulesReadTableOverride(t *testing.T) {
	t.Setenv("STORAGE_RULES", "on")
	s := newPresetSite(t)
	s.must(t, "PUT", "/resources/data", `{"kind":"sqlite","rules":{"policies":[{"command":"read","using":"true"}]},"tables":{"closed":{"rules":{"policies":[]}}}}`, s.key, 201)
	for _, table := range []string{"open", "closed"} {
		s.must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE " + table + "(body TEXT)"}, s.key, 200)
		s.must(t, "POST", "/sqlite/data/execute", map[string]string{"sql": "INSERT INTO " + table + " VALUES('secret')"}, s.key, 200)
		for _, suffix := range []string{"", "/1"} {
			status := 200
			if table == "closed" {
				status = 403
			}
			r := s.must(t, "GET", "/sqlite/data/tables/"+table+"/rows"+suffix, nil, s.callers["anon"], status)
			if table == "closed" && r.json(t)["code"] != "forbidden" {
				t.Fatal(string(r.body))
			}
		}
	}
}

func TestStorageRulesReadAuthenticationResponses(t *testing.T) {
	t.Setenv("STORAGE_RULES", "on")
	s := newPresetSite(t)
	s.must(t, "PUT", "/resources/data", `{"kind":"sqlite","preset":"board"}`, s.key, 201)
	s.must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE entries(body TEXT)"}, s.key, 200)
	// Pin the existing preset bodies before conversion.
	expected := map[string]resp{}
	for _, suffix := range []string{"", "/1"} {
		expected["anon"+suffix] = s.must(t, "GET", "/sqlite/data/tables/entries/rows"+suffix, nil, s.callers["anon"], 401)
	}
	if _, err := s.a.database.Exec(`UPDATE users SET suspended_at=now() WHERE id=$1`, s.aliceID); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "/1"} {
		expected["alice"+suffix] = s.must(t, "GET", "/sqlite/data/tables/entries/rows"+suffix, nil, s.callers["alice"], 403)
	}
	converted := ruleengine.Convert(ruleengine.ConvertInput{Kind: "sqlite", Default: ruleengine.Matrix{Read: "signed-in", Add: "signed-in", Edit: "signed-in", Delete: "owner"}})
	if len(converted.Manual) != 0 {
		t.Fatal(converted.Manual)
	}
	s.must(t, "PUT", "/resources/data", map[string]any{"kind": "sqlite", "rules": json.RawMessage(ruleengine.EncodeSet(converted.Default))}, s.key, 200)
	for _, who := range []string{"anon", "alice"} {
		for _, suffix := range []string{"", "/1"} {
			want := expected[who+suffix]
			got := s.must(t, "GET", "/sqlite/data/tables/entries/rows"+suffix, nil, s.callers[who], want.status)
			if string(got.body) != string(want.body) {
				t.Fatalf("%s: %s != %s", who, got.body, want.body)
			}
			// Execution-stage failures use the same HTTP serializer as authorization.
			code, message := "account_suspended", ""
			if who == "anon" {
				code, message = "sign_in_required", "sign in to this site first"
			}
			w := httptest.NewRecorder()
			writeTableError(w, &appstorage.Error{Code: code, Message: message})
			if w.Code != want.status || w.Body.String() != string(want.body) {
				t.Fatalf("execution: %d %s; want %d %s", w.Code, w.Body.String(), want.status, want.body)
			}
		}
	}
}
