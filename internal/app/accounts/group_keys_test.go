package accounts

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	"github.com/vsriram/simple-host/internal/testdb"
)

type keysStub struct {
	err    error
	member string
}

func (h keysStub) Binding(context.Context, sqlkit.Querier, string) (string, string, error) {
	return "team", h.member, h.err
}
func (h keysStub) Identity(context.Context, sqlkit.Querier, string, string) (model.TeamIdentity, error) {
	return model.TeamIdentity{}, h.err
}
func (h keysStub) WriteRefusal(context.Context, sqlkit.Querier, string) (int, string, string, error) {
	return 0, "", "", h.err
}
func (h keysStub) FileLinkAllows(context.Context, sqlkit.Querier, model.Site, string, string, string) bool {
	return h.err == nil
}
func (h keysStub) Messages() (string, string) { return "scope", "inactive" }

func TestGroupKeysNilAndErrors(t *testing.T) {
	db, rec := testdb.OpenRecording(testdb.Fresh(t))
	t.Cleanup(func() { db.Close() })
	t.Cleanup(func() { SetGroupHooks(GroupHooks{}) })
	ctx := context.Background()
	var member string
	if err := db.QueryRow(`INSERT INTO users (username) VALUES ('group-keys@example.com') RETURNING id`).Scan(&member); err != nil {
		t.Fatal(err)
	}
	const key = "shk_group-keys-test"
	if _, err := db.Exec(`INSERT INTO api_keys (key_hash,user_id,scope) VALUES ($1,$2,'team')`, repo.HashAPIKey(key), member); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	SetGroupHooks(GroupHooks{})
	rec.Reset()
	if _, err := s.GetUserByAPIKey(ctx, key, time.Hour); err != sql.ErrNoRows {
		t.Fatalf("nil key hook: %v", err)
	}
	if isTeam, live, _, err := s.TeamScopeOfKey(ctx, key); isTeam || live || err != nil {
		t.Fatalf("nil scope: %v %v %v", isTeam, live, err)
	}
	if _, err := s.ResolveTeamIdentity(ctx, member, "team"); err != sql.ErrNoRows {
		t.Fatalf("nil identity: %v", err)
	}
	for _, kind := range []string{"team", "event"} {
		if s.FileLinkScopeAllows(ctx, model.Site{}, kind, "group", member) {
			t.Fatalf("nil hook allowed %s link", kind)
		}
	}
	if status, code, msg, err := s.GroupWriteRefusal(ctx, "team"); status != 0 || code != "" || msg != "" || err != nil {
		t.Fatalf("nil write refusal: %d %s %s %v", status, code, msg, err)
	}
	for _, stmt := range rec.Statements() {
		if strings.Contains(stmt, "event_") || strings.Contains(stmt, "FROM events") {
			t.Fatalf("nil hook queried product: %s", stmt)
		}
	}
	want := errors.New("key hook failure")
	SetGroupHooks(GroupHooks{Keys: keysStub{err: want, member: member}})
	if _, err := s.GetUserByAPIKey(ctx, key, time.Hour); !errors.Is(err, want) {
		t.Fatalf("binding error: %v", err)
	}
	if _, _, _, err := s.TeamScopeOfKey(ctx, key); !errors.Is(err, want) {
		t.Fatalf("scope error: %v", err)
	}
	if _, err := s.ResolveTeamIdentity(ctx, member, "team"); !errors.Is(err, want) {
		t.Fatalf("identity error: %v", err)
	}
	if _, _, _, err := s.GroupWriteRefusal(ctx, "team"); !errors.Is(err, want) {
		t.Fatalf("write error: %v", err)
	}
	if s.FileLinkScopeAllows(ctx, model.Site{}, "team", "team", member) {
		t.Fatal("failed link hook allowed access")
	}
	if scope, inactive := s.GroupKeyMessages(); scope != "scope" || inactive != "inactive" {
		t.Fatalf("messages: %q %q", scope, inactive)
	}
	// The platform still checks that a binding belongs to the key's person.
	SetGroupHooks(GroupHooks{Keys: keysStub{member: "someone else"}})
	if _, err := s.GetUserByAPIKey(ctx, key, time.Hour); !errors.Is(err, model.ErrTeamKeyInactive) {
		t.Fatalf("mismatched binding: %v", err)
	}
}
