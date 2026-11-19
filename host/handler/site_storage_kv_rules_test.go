package handler

import (
	appstorage "github.com/vsriram/simple-host/host/app/storage"
	"net/http/httptest"
	"testing"
)

func TestStorageKVRulesHTTP(t *testing.T) {
	t.Setenv("STORAGE_RULES", "on")
	s := newPresetSite(t)
	s.must(t, "PUT", "/resources/data", `{"kind":"kv","rules":{"policies":[{"command":"read","using":"key contains \"public\""},{"command":"add","check":"value == \"1\""},{"command":"edit","using":"auth.signed_in","check":"value == \"2\""},{"command":"delete","using":"auth.signed_in"}]}}`, s.key, 201)
	s.must(t, "PUT", "/kv/data/keys/public", `{"value":1}`, s.callers["alice"], 200)
	for _, tc := range []struct {
		method, path, body, who, code string
		status                        int
	}{
		{"GET", "/kv/data/keys/public", "", "anon", "", 200},
		{"PUT", "/kv/data/keys/new", `{"value":9}`, "alice", "forbidden", 403},
		{"PUT", "/kv/data/keys/public", `{"value":9}`, "alice", "key_exists", 409},
		{"DELETE", "/kv/data/keys/public", "", "anon", "sign_in_required", 401},
		{"GET", "/kv/data/keys/missing", "", "anon", "key_not_found", 404},
	} {
		var body any
		if tc.body != "" {
			body = tc.body
		}
		r := s.must(t, tc.method, tc.path, body, s.callers[tc.who], tc.status)
		if tc.code != "" && r.json(t)["code"] != tc.code {
			t.Fatal(string(r.body))
		}
	}
	s.must(t, "PUT", "/kv/data/keys/public", `{"value":2}`, s.callers["alice"], 200)
	s.must(t, "DELETE", "/kv/data/keys/public", nil, s.callers["alice"], 200)
}

func TestStorageKVRulesErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
	}{{"rules_invalid", 403}, {"internal_error", 500}, {"sign_in_required", 401}, {"account_suspended", 403}, {"forbidden", 403}, {"spam_check_required", 403}, {"spam_check_failed", 403}, {"spam_check_unavailable", 403}} {
		t.Run(tc.code, func(t *testing.T) {
			w := httptest.NewRecorder()
			if !writeKVAdmissionError(w, &appstorage.Error{Code: tc.code, Message: "refused"}) {
				t.Fatal("unhandled")
			}
			checkAPIError(t, w, tc.status, tc.code)
		})
	}
}
