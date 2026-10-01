package handler

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/geoip"
	"github.com/vsriram/simple-host/internal/geoip/geoiptest"
)

// buildGeo writes a tiny local geo database mapping ip to iso, for a test to
// hand to SetSignupGeoBlock directly.
func buildGeo(t *testing.T, ip, iso string) *geoip.DB {
	t.Helper()
	dir := t.TempDir()
	if err := geoiptest.Write(filepath.Join(dir, geoip.CityFile), "DBIP-City-Lite", map[string]geoiptest.Record{
		ip + "/32": {"country": geoiptest.Record{"iso_code": iso, "names": geoiptest.Record{"en": iso}}},
	}); err != nil {
		t.Fatal(err)
	}
	geo := geoip.Open(dir)
	t.Cleanup(geo.Close)
	return geo
}

// TestOAuthCallbackGeoBlock covers the OAuth sign-in/sign-up country block:
// every callback attempt from a blocked country is refused before the
// provider round-trip or any state lookup, new identity or one that would
// otherwise resolve to an existing account alike; an unresolved country or
// an empty knob must not trip it (the ordinary invalid-state refusal shows
// through instead, proving the new check did not fire).
func TestOAuthCallbackGeoBlock(t *testing.T) {
	a := newPrivateApp(t)
	oh := a.withOAuth(t)
	const blockedIP = "198.51.100.13"
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	ctx := context.Background()

	callback := func(code, ip string) resp {
		return a.at(t, "GET", pcSiteDomain, "/v1/auth/oauth/google/callback?state=bogus-state&code="+code, nil, map[string]string{"X-Forwarded-For": ip})
	}

	t.Run("new identity from a blocked country is refused, nothing created", func(t *testing.T) {
		oh.SetSignupGeoBlock(buildGeo(t, blockedIP, "IQ"), []string{"IQ"})
		code := "newperson-" + stamp
		email := code + "@example.test"
		r := callback(code, blockedIP)
		if r.status != http.StatusForbidden || !strings.Contains(string(r.body), signupBlockedMessage) {
			t.Fatalf("got %d %q, want 403 with the blocked message", r.status, r.body)
		}
		var n int
		if err := a.database.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE username = $1`, email).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatal("a blocked callback must not create a user row")
		}
	})

	t.Run("identity that would resolve to an existing account is also refused, account untouched", func(t *testing.T) {
		code := "existingperson-" + stamp
		email := code + "@example.test"
		key, err := auth.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		existing, err := db.CreateUser(ctx, a.database, email, key, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = a.database.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, existing.ID) })

		oh.SetSignupGeoBlock(buildGeo(t, blockedIP, "IQ"), []string{"IQ"})
		r := callback(code, blockedIP)
		if r.status != http.StatusForbidden || !strings.Contains(string(r.body), signupBlockedMessage) {
			t.Fatalf("got %d %q, want 403 with the blocked message", r.status, r.body)
		}
		fresh, err := db.GetUserByAPIKey(ctx, a.database, key)
		if err != nil || fresh.ID != existing.ID {
			t.Fatalf("existing account changed by a refused sign-in attempt: %v %+v", err, fresh)
		}
	})

	t.Run("unresolved country is not blocked", func(t *testing.T) {
		oh.SetSignupGeoBlock(buildGeo(t, blockedIP, "IQ"), []string{"IQ"})
		// A different, unmapped IP: the geo check must pass through, leaving
		// the ordinary invalid-state refusal (this test sends no real state).
		r := callback("unknown-"+stamp, "203.0.113.90")
		if r.status != http.StatusBadRequest || strings.Contains(string(r.body), signupBlockedMessage) {
			t.Fatalf("got %d %q, want the ordinary invalid-state refusal, not the geo block", r.status, r.body)
		}
	})

	t.Run("empty block list is off", func(t *testing.T) {
		oh.SetSignupGeoBlock(nil, nil)
		r := callback("off-"+stamp, blockedIP)
		if r.status != http.StatusBadRequest || strings.Contains(string(r.body), signupBlockedMessage) {
			t.Fatalf("got %d %q, want the ordinary invalid-state refusal, not the geo block", r.status, r.body)
		}
	})
}
