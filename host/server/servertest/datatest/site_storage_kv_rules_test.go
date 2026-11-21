package datatest

import (
	"github.com/vsriram/simple-host/host/server/servertest"
	"testing"
)

func TestStorageKVRulesHTTP(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	s.Must(t, "PUT", "/resources/data", `{"kind":"kv","rules":{"policies":[{"command":"read","using":"key contains \"public\""},{"command":"add","check":"value == \"1\""},{"command":"edit","using":"auth.signed_in","check":"value == \"2\""},{"command":"delete","using":"auth.signed_in"}]}}`, s.Key, 201)
	s.Must(t, "PUT", "/kv/data/keys/public", `{"value":1}`, s.Callers["alice"], 200)
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
		r := s.Must(t, tc.method, tc.path, body, s.Callers[tc.who], tc.status)
		if tc.code != "" && r.JSON(t)["code"] != tc.code {
			t.Fatal(string(r.Body))
		}
	}
	s.Must(t, "PUT", "/kv/data/keys/public", `{"value":2}`, s.Callers["alice"], 200)
	s.Must(t, "DELETE", "/kv/data/keys/public", nil, s.Callers["alice"], 200)
}
