package db

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Ping(); err != nil {
		t.Skipf("postgres not reachable: %v", err)
	}
	return database
}

func hashFixture(t *testing.T, database *sql.DB) (userID, siteID string) {
	t.Helper()
	ctx := context.Background()
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	user, err := CreateUser(ctx, database, "hash-test-"+stamp+"@example.com", "key-"+stamp, false)
	if err != nil {
		t.Fatal(err)
	}
	site, err := CreateSite(ctx, database, user.ID, "hashtest"+stamp[len(stamp)-8:], "http://example.test/"+stamp, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = database.ExecContext(ctx, `DELETE FROM visitor_establish_tokens WHERE user_id = $1`, user.ID)
		_, _ = database.ExecContext(ctx, `DELETE FROM visitor_sessions WHERE user_id = $1`, user.ID)
		_, _ = database.ExecContext(ctx, `DELETE FROM oauth_states WHERE site_id = $1`, site.ID)
		_, _ = database.ExecContext(ctx, `DELETE FROM sites WHERE id = $1`, site.ID)
		_, _ = database.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, user.ID)
	})
	return user.ID, site.ID
}

func TestInsertVisitorSessionStoresHashNotToken(t *testing.T) {
	database := testDB(t)
	ctx := context.Background()
	userID, siteID := hashFixture(t, database)

	token, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := InsertVisitorSession(ctx, database, token, userID, siteID, "sites.example.test", now.Add(time.Hour), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	var stored string
	if err := database.QueryRowContext(ctx, `SELECT encode(id, 'hex') FROM visitor_sessions WHERE user_id = $1`, userID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token {
		t.Fatalf("stored session id equals the cookie token — replayable")
	}
	if stored != HashTokenHex(token) {
		t.Fatalf("stored %s, want HashTokenHex %s", stored, HashTokenHex(token))
	}

	got, err := GetVisitorSession(ctx, database, token)
	if err != nil {
		t.Fatalf("lookup by presented token: %v", err)
	}
	if got.UserID != userID || got.SiteID != siteID {
		t.Fatalf("looked up %+v", got)
	}
	if hex.EncodeToString(got.ID) == token {
		t.Fatal("returned ID is the presented token")
	}

	if _, err := GetVisitorSession(ctx, database, stored); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("presenting the stored digest as the cookie: got %v, want ErrNoRows", err)
	}
	other, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetVisitorSession(ctx, database, other); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("random token: got %v, want ErrNoRows", err)
	}
}

func TestInsertEstablishTokenStoresHashAndIsSingleUse(t *testing.T) {
	database := testDB(t)
	ctx := context.Background()
	userID, siteID := hashFixture(t, database)

	once, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := InsertEstablishToken(ctx, database, once, userID, siteID, "sites.example.test", "https://sites.example.test/back", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	var stored string
	if err := database.QueryRowContext(ctx, `SELECT once FROM visitor_establish_tokens WHERE user_id = $1`, userID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == once {
		t.Fatal("stored once equals the URL value — replayable")
	}
	if stored != HashTokenHex(once) {
		t.Fatalf("stored %s, want %s", stored, HashTokenHex(once))
	}

	tok, err := ConsumeEstablishToken(ctx, database, once, "sites.example.test")
	if err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if tok.UserID != userID || tok.SiteID != siteID {
		t.Fatalf("consumed %+v", tok)
	}
	if _, err := ConsumeEstablishToken(ctx, database, once, "sites.example.test"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("second consume: got %v, want ErrNoRows", err)
	}
	if _, err := ConsumeEstablishToken(ctx, database, stored, "sites.example.test"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("presenting stored digest as once: got %v, want ErrNoRows", err)
	}
}

func TestInsertOAuthStateStoresHashAndLookup(t *testing.T) {
	database := testDB(t)
	ctx := context.Background()
	_, siteID := hashFixture(t, database)

	state, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := InsertOAuthState(ctx, database, state, "google", "verifier", "https://sites.example.test/back", "sites.example.test", sql.NullString{String: siteID, Valid: true}, "site", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	var stored string
	if err := database.QueryRowContext(ctx, `SELECT state FROM oauth_states WHERE site_id = $1`, siteID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == state {
		t.Fatal("stored state equals the provider state — replayable")
	}
	if stored != HashTokenHex(state) {
		t.Fatalf("stored %s, want %s", stored, HashTokenHex(state))
	}

	got, err := ConsumeOAuthState(ctx, database, state)
	if err != nil {
		t.Fatalf("consume by presented state: %v", err)
	}
	if got.Provider != "google" || got.SiteID.String != siteID {
		t.Fatalf("consumed %+v", got)
	}
	if _, err := ConsumeOAuthState(ctx, database, state); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("second consume: got %v, want ErrNoRows", err)
	}
	if _, err := ConsumeOAuthState(ctx, database, stored); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("presenting stored digest as state: got %v, want ErrNoRows", err)
	}
	forged, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ConsumeOAuthState(ctx, database, forged); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("forged state: got %v, want ErrNoRows", err)
	}
}

func TestInsertRejectsShortToken(t *testing.T) {
	database := testDB(t)
	ctx := context.Background()
	userID, siteID := hashFixture(t, database)
	now := time.Now()

	if err := InsertVisitorSession(ctx, database, "deadbeef", userID, siteID, "h", now.Add(time.Hour), now.Add(time.Hour)); !errors.Is(err, ErrTokenTooShort) {
		t.Fatalf("short session token: got %v, want ErrTokenTooShort", err)
	}
	if err := InsertEstablishToken(ctx, database, "short", userID, siteID, "h", "https://x/", now.Add(time.Minute)); !errors.Is(err, ErrTokenTooShort) {
		t.Fatalf("short once: got %v, want ErrTokenTooShort", err)
	}
	if err := InsertOAuthState(ctx, database, "aa", "google", "v", "https://x/", "h", sql.NullString{String: siteID, Valid: true}, "site", now.Add(time.Minute)); !errors.Is(err, ErrTokenTooShort) {
		t.Fatalf("short state: got %v, want ErrTokenTooShort", err)
	}
}
