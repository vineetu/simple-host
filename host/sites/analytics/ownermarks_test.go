package analytics

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// A person's hit from a marked address on the marked account's site is the
// owner's; another address, a bot, or another account's site is not.
func TestViewHitOwnerMark(t *testing.T) {
	i := NewIngester(nil, "", "k", "sites.example.app", "example.app").WithSalt("t")
	const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15"
	m := &attrMaps{
		domainToID: map[string]string{"brand.com": "s-u", "other.com": "s-v"},
		siteOwner:  map[string]string{"s-u": "U", "s-v": "V"},
		ownerMarks: map[string]struct{}{"U|" + hex.EncodeToString(hashIP("t", "203.0.113.9")): {}},
	}
	line := func(host, ip, ua string) logLine {
		return logLine{tsStr: "2026-11-02T10:00:00+00:00", host: host, status: "200", method: "GET", uri: "/", remoteAddr: ip, ua: ua}
	}
	for _, tc := range []struct {
		label string
		l     logLine
		want  Class
	}{
		{"owner from the marked address", line("brand.com", "203.0.113.9", browserUA), ClassOwner},
		{"another address stays a person", line("brand.com", "203.0.113.10", browserUA), ClassPerson},
		{"a bot from the marked address stays a bot", line("brand.com", "203.0.113.9", "curl/8.4.0"), ClassBot},
		{"another account's site stays a person", line("other.com", "203.0.113.9", browserUA), ClassPerson},
	} {
		h, ok := i.viewHit(tc.l, m)
		if !ok {
			t.Fatalf("%s: not a hit", tc.label)
		}
		if h.key.class != tc.want {
			t.Errorf("%s: class %q, want %q", tc.label, h.key.class, tc.want)
		}
		if !bytes.Equal(h.ipHash, hashIP("t", tc.l.remoteAddr)) {
			t.Errorf("%s: ip_hash differs from hashIP", tc.label)
		}
	}
}

// The exported hash helpers are the ingester's own.
func TestExportedHashMatchesIngester(t *testing.T) {
	if VisitorSalt("k") != visitorSalt("k") {
		t.Fatal("VisitorSalt differs from visitorSalt")
	}
	if !bytes.Equal(HashIP("t", "203.0.113.9"), hashIP("t", "203.0.113.9")) {
		t.Fatal("HashIP differs from hashIP")
	}
	derived := NewIngester(nil, "", "k", "", "")
	if NewOwnerMarks(nil, "k", "").salt != derived.salt {
		t.Fatal("NewOwnerMarks without an explicit salt must derive the ingester's")
	}
	if NewOwnerMarks(nil, "k", "x").salt != derived.WithSalt("x").salt {
		t.Fatal("NewOwnerMarks with an explicit salt must use it")
	}
}

// Mark writes one row with the ingester's hash, is throttled in memory for
// the same (user, address), and never writes loopback. Needs DB_DSN with
// owner_visitor_marks applied.
func TestOwnerMarksMark(t *testing.T) {
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
	ctx := context.Background()
	var userID string
	if err := database.QueryRowContext(ctx, `INSERT INTO users (username) VALUES ($1) RETURNING id`,
		"ownermarks-"+time.Now().Format("20060102150405.000000000")+"@example.com").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = database.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	m := NewOwnerMarks(database, "k", "t")
	const ip = "198.51.100.7"
	if err := m.Mark(ctx, userID, ip); err != nil {
		t.Fatal(err)
	}
	var hash []byte
	var seen time.Time
	var n int
	row := func() {
		t.Helper()
		if err := database.QueryRowContext(ctx, `SELECT count(*) FROM owner_visitor_marks WHERE user_id = $1`, userID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
		if err := database.QueryRowContext(ctx, `SELECT ip_hash, last_seen FROM owner_visitor_marks WHERE user_id = $1`, userID).Scan(&hash, &seen); err != nil {
			t.Fatal(err)
		}
	}
	row()
	if n != 1 || !bytes.Equal(hash, HashIP("t", ip)) {
		t.Fatalf("after Mark: %d rows, hash matches = %v", n, bytes.Equal(hash, HashIP("t", ip)))
	}
	first := seen

	// Second call within the throttle: no write (last_seen unchanged).
	if _, err := database.ExecContext(ctx, `UPDATE owner_visitor_marks SET last_seen = last_seen - interval '1 hour' WHERE user_id = $1`, userID); err != nil {
		t.Fatal(err)
	}
	if err := m.Mark(ctx, userID, ip); err != nil {
		t.Fatal(err)
	}
	row()
	if n != 1 || !seen.Before(first) {
		t.Fatalf("throttled Mark touched the row: rows=%d last_seen=%v first=%v", n, seen, first)
	}

	// Once the throttle has lapsed the same pair is written again (upsert).
	m.mu.Lock()
	for k := range m.recent {
		m.recent[k] = time.Now().Add(-markThrottle - time.Second)
	}
	m.mu.Unlock()
	if err := m.Mark(ctx, userID, ip); err != nil {
		t.Fatal(err)
	}
	row()
	if n != 1 || !seen.After(first.Add(-time.Minute)) {
		t.Fatalf("lapsed throttle did not upsert: rows=%d last_seen=%v", n, seen)
	}

	// Loopback and empty inputs write nothing.
	for _, c := range [][2]string{{userID, "127.0.0.1"}, {userID, "::1"}, {userID, ""}, {"", ip}} {
		if err := m.Mark(ctx, c[0], c[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM owner_visitor_marks WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("loopback or empty input wrote a row: %d rows", n)
	}
}
