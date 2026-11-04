package handler

import (
	"errors"
	"github.com/vsriram/simple-host/internal/app/legacy"
	"testing"
)

func TestPersonalBlockedPatchAndRestoreResponse(t *testing.T) {
	s := newKindsSite(t, false)
	s.declare(t, "habits", map[string]any{"kind": "personal"})
	const path = "/v1/sites/shop/data/habits"
	if r := s.visitor(t, "PUT", path, map[string]any{"level": 1}); r.status != 200 {
		t.Fatalf("save: %d %s", r.status, r.body)
	}
	if r := s.owner(t, "POST", "/v1/sites/shop/savers/block", map[string]any{"email": s.vic.email}); r.status != 200 {
		t.Fatalf("block: %d %s", r.status, r.body)
	}
	for _, method := range []string{"PATCH", "POST"} {
		target := path
		if method == "POST" {
			target += "/history/1/restore"
		}
		r := s.visitor(t, method, target, map[string]any{"ops": []any{map[string]any{"op": "set", "path": "level", "value": 2}}})
		const want = "{\"error\":\"the owner of this site has not allowed this account to save here\",\"code\":\"not_allowed_to_save\"}\n"
		if r.status != 403 || string(r.body) != want {
			t.Fatalf("%s: %d %s, want 403 %s", method, r.status, r.body, want)
		}
	}
}

func TestCollectionClaimMissingRowResponse(t *testing.T) {
	s := newKindsSite(t, false)
	// Suppress the claim INSERT, then let the lookup see no row. This reproduces
	// a pending claim disappearing between the conflicting INSERT and SELECT.
	if _, err := s.a.database.Exec(`CREATE FUNCTION w4_missing_claim() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$;
 CREATE TRIGGER w4_missing_claim BEFORE INSERT ON idempotency_keys FOR EACH ROW EXECUTE FUNCTION w4_missing_claim()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := s.a.database.Exec(`DROP TRIGGER w4_missing_claim ON idempotency_keys; DROP FUNCTION w4_missing_claim()`); err != nil {
			t.Error(err)
		}
	})
	for _, kind := range []string{"entries", "board", "private"} {
		t.Run(kind, func(t *testing.T) {
			declareKind := kind
			if kind == "private" {
				declareKind = "entries"
			}
			s.declare(t, kind, map[string]any{"kind": declareKind})
			if kind == "private" {
				r := s.owner(t, "PUT", "/v1/sites/shop/collections/private/privacy", map[string]any{"private": true})
				if r.status != 200 {
					t.Fatalf("privacy: %d %s", r.status, r.body)
				}
			}
			r := s.a.at(t, "POST", s.dom, "/v1/sites/shop/collections/"+kind, map[string]any{"text": "hello"}, browser(s.dom, s.vicCooky, "Idempotency-Key", "claim-missing"))
			const want = "{\"error\":\"internal server error\"}\n"
			if r.status != 500 || string(r.body) != want {
				t.Fatalf("claim failure: %d %s, want 500 %s", r.status, r.body, want)
			}
		})
	}
}

// Validation moved from the adapter with its wire-format decoder.
func TestLegacyBodyValidationOrder(t *testing.T) {
	for _, tc := range []struct{ branch, body, code string }{
		{"public", "", "invalid_collection_body"},
		{"public", "{", "invalid_collection_body"},
		{"public", `"\u0000"`, "invalid_json"},
		{"private", `["\u0000"]`, "private_not_object"},
		{"board", `{"text":"\u0000"}`, "invalid_json"},
		{"personal", `null`, "personal_not_object"},
	} {
		_, err := decodeLegacyBody([]byte(tc.body), tc.branch, false, nil)
		var opErr *legacy.Error
		if !errors.As(err, &opErr) || opErr.Code != tc.code {
			t.Errorf("%s %q: got %v, want %s", tc.branch, tc.body, err, tc.code)
		}
	}
}
