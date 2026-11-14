package accounts

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	"github.com/vsriram/simple-host/internal/testdb"
)

type targetStub struct {
	q     sqlkit.Querier
	err   error
	calls int
}

func (h *targetStub) Selected(_ context.Context, q sqlkit.Querier, _ string) (sql.NullString, error) {
	h.q = q
	h.calls++
	return sql.NullString{String: "choice", Valid: true}, h.err
}
func (h *targetStub) Select(_ context.Context, q sqlkit.Querier, _, _, _ string) (bool, error) {
	h.q = q
	h.calls++
	return false, h.err
}
func (h *targetStub) Labels(_ context.Context, q sqlkit.Querier, _ string) (map[string][]model.OAuthConnectionTeam, error) {
	h.q = q
	h.calls++
	return nil, h.err
}

func TestConnectorTargetHooks(t *testing.T) {
	dsn := testdb.Fresh(t)
	db, rec := testdb.OpenRecording(dsn)
	t.Cleanup(func() { db.Close() })
	t.Cleanup(func() { SetGroupHooks(GroupHooks{}) })
	ctx := context.Background()
	var user, grant string
	if err := db.QueryRow(`INSERT INTO users (username) VALUES ('target-hooks@example.com') RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO oauth_clients (client_id,client_name,redirect_uris) VALUES ('target-hooks','Test',ARRAY['https://example.com/cb'])`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO oauth_grants (user_id,client_id,scope,resource,selected_team_id) VALUES ($1,'target-hooks','sites events','https://example.com/mcp','00000000-0000-4000-8000-000000000011') RETURNING id`, user).Scan(&grant); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO oauth_tokens (token_hash,grant_id,kind,expires_at) VALUES ('target-token',$1,'access',now()+interval '1 hour')`, grant); err != nil {
		t.Fatal(err)
	}
	s := New(db) // Constructed before installation: the hook must be read at call time.
	SetGroupHooks(GroupHooks{})
	rec.Reset()
	g, err := s.GetOAuthGrantByID(ctx, grant)
	if err != nil || g.SelectedTeamID.Valid {
		t.Fatalf("nil grant: %v %v", g, err)
	}
	tok, err := s.GetOAuthToken(ctx, "target-token")
	if err != nil || tok.SelectedTeamID.Valid {
		t.Fatalf("nil token: %v %v", tok, err)
	}
	if ok, err := s.SelectOAuthTeam(ctx, grant, user, "anything"); err != nil || ok {
		t.Fatalf("nil select: %v %v", ok, err)
	}
	if labels, err := s.OAuthConnectionTeams(ctx, user); err != nil || len(labels) != 0 {
		t.Fatalf("nil labels: %v %v", labels, err)
	}
	for _, stmt := range rec.Statements() {
		if strings.Contains(stmt, "selected_team_id") || strings.Contains(stmt, "hack_grant_teams") || strings.Contains(stmt, "event_teams") {
			t.Fatalf("platform query: %s", stmt)
		}
	}
	hook := &targetStub{}
	SetGroupHooks(GroupHooks{Target: hook})
	g, err = s.GetOAuthGrantByID(ctx, grant)
	if err != nil || g.SelectedTeamID.String != "choice" || hook.q != db {
		t.Fatalf("grant hook: %v %v", g, err)
	}
	tok, err = s.GetOAuthToken(ctx, "target-token")
	if err != nil || tok.SelectedTeamID.String != "choice" {
		t.Fatalf("token hook: %v %v", tok, err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := getOAuthToken(ctx, tx, "target-token"); err != nil || hook.q != tx {
		t.Fatalf("refresh must use caller transaction: %v", err)
	}
	want := errors.New("target failed")
	hook.err = want
	if _, err := s.GetOAuthGrantByID(ctx, grant); !errors.Is(err, want) {
		t.Fatalf("grant error: %v", err)
	}
	if _, err := s.GetOAuthToken(ctx, "target-token"); !errors.Is(err, want) {
		t.Fatalf("token error: %v", err)
	}
	if _, err := s.SelectOAuthTeam(ctx, grant, user, "anything"); !errors.Is(err, want) {
		t.Fatalf("select error: %v", err)
	}
	if _, err := s.OAuthConnectionTeams(ctx, user); !errors.Is(err, want) {
		t.Fatalf("labels error: %v", err)
	}
	before := hook.calls
	if _, err := s.GetOAuthToken(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if _, err := s.GetOAuthGrantByID(ctx, "00000000-0000-4000-8000-000000000099"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if hook.calls != before {
		t.Fatal("hook called after failed platform lookup")
	}
}
