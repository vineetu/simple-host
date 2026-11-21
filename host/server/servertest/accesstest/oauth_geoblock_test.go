package accesstest

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/web"
)

// buildGeo supplies a local country lookup mapping ip to iso.
func buildGeo(t *testing.T, ip, iso string) countryLookup {
	t.Helper()
	return countryLookup{ip: iso}
}

// TestOAuthCallbackGeoBlock covers the OAuth sign-in/sign-up country block:
// every callback attempt from a blocked country is refused before the
// provider round-trip or any state lookup, new identity or one that would
// otherwise resolve to an existing account alike; an unresolved country or
// an empty knob must not trip it (the ordinary invalid-state refusal shows
// through instead, proving the new check did not fire).
func TestOAuthCallbackGeoBlock(t *testing.T) {
	a := servertest.NewPrivateApp(t)
	oh := a.WithOAuth(t)
	const blockedIP = "198.51.100.13"
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	ctx := context.Background()

	callback := func(code, ip string) servertest.Resp {
		return a.At(t, "GET", servertest.PcSiteDomain, "/v1/auth/oauth/google/callback?state=bogus-state&code="+code, nil, map[string]string{"X-Forwarded-For": ip})
	}

	t.Run("new identity from a blocked country is refused, nothing created", func(t *testing.T) {
		oh.SetSignupGeoBlock(accountapp.NewSignupGeoBlock(buildGeo(t, blockedIP, "IQ"), []string{"IQ"}))
		code := "newperson-" + stamp
		email := code + "@example.test"
		r := callback(code, blockedIP)
		if r.Status != http.StatusForbidden || !strings.Contains(string(r.Body), web.SignupBlockedMessage) {
			t.Fatalf("got %d %q, want 403 with the blocked message", r.Status, r.Body)
		}
		var n int
		if err := a.Database.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE username = $1`, email).Scan(&n); err != nil {
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
		existing, err := accountsrepo.CreateUser(ctx, a.Database, email, key, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = a.Database.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, existing.ID) })

		oh.SetSignupGeoBlock(accountapp.NewSignupGeoBlock(buildGeo(t, blockedIP, "IQ"), []string{"IQ"}))
		r := callback(code, blockedIP)
		if r.Status != http.StatusForbidden || !strings.Contains(string(r.Body), web.SignupBlockedMessage) {
			t.Fatalf("got %d %q, want 403 with the blocked message", r.Status, r.Body)
		}
		fresh, err := accountapp.New(a.Database).GetUserByAPIKey(ctx, key, 180*24*time.Hour)
		if err != nil || fresh.ID != existing.ID {
			t.Fatalf("existing account changed by a refused sign-in attempt: %v %+v", err, fresh)
		}
	})

	t.Run("unresolved country is not blocked", func(t *testing.T) {
		oh.SetSignupGeoBlock(accountapp.NewSignupGeoBlock(buildGeo(t, blockedIP, "IQ"), []string{"IQ"}))
		// A different, unmapped IP: the geo check must pass through, leaving
		// the ordinary invalid-state refusal (this test sends no real state).
		r := callback("unknown-"+stamp, "203.0.113.90")
		if r.Status != http.StatusBadRequest || strings.Contains(string(r.Body), web.SignupBlockedMessage) {
			t.Fatalf("got %d %q, want the ordinary invalid-state refusal, not the geo block", r.Status, r.Body)
		}
	})

	t.Run("empty block list is off", func(t *testing.T) {
		oh.SetSignupGeoBlock(accountapp.NewSignupGeoBlock(nil, nil))
		r := callback("off-"+stamp, blockedIP)
		if r.Status != http.StatusBadRequest || strings.Contains(string(r.Body), web.SignupBlockedMessage) {
			t.Fatalf("got %d %q, want the ordinary invalid-state refusal, not the geo block", r.Status, r.Body)
		}
	})
}
