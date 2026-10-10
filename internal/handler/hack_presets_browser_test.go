package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/config"
)

// TestServeHackPresetsBrowser stands up a Simple Hack instance (hack mode,
// team keys, captured sign-in mail, one open event with a team and its key)
// for the browser proof of storage presets on Simple Hack:
//
//	HACK_PRESETS_FIXTURE=/tmp/f.json DB_DSN=... go test ./internal/handler/ -run TestServeHackPresetsBrowser
//	node scripts/e2e-hack-presets.mjs /tmp/f.json
//
// A fixture, not an assertion: the script asserts, then POSTs /_fixture/stop.
func TestServeHackPresetsBrowser(t *testing.T) {
	file := os.Getenv("HACK_PRESETS_FIXTURE")
	if file == "" {
		t.Skip("HACK_PRESETS_FIXTURE unset")
	}
	mb := &mailbox{}
	a := newTeamSiteAppMailer(t, mb)
	a.sites.emailLimiter = newRateLimiter(1000, 0)
	a.sites.visitorAuthLimiter = newRateLimiter(1000, 0)
	a.sites.storageIPLimiter = newRateLimiter(100000, 0)
	RegisterUIRoutes(a.mux, a.srv.URL, a.sites)
	oauth := NewOAuthHandler(a.database, config.Config{PublicBaseURL: a.srv.URL, ResendAPIKey: "test-mail-sink"})
	oauth.Register(a.mux)
	org, member := a.newPerson(t, "hfix-org"), a.newPerson(t, "hfix-member")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	team, _ := a.startTeam(t, slug, "Wall", member)
	markReady(t, a.certDir, slug)
	key := a.teamKey(t, slug, member)
	a.mux.HandleFunc("GET /_fixture/mail", func(w http.ResponseWriter, r *http.Request) {
		mb.mu.Lock()
		defer mb.mu.Unlock()
		writeJSON(w, 200, map[string]string{"code": mb.codes[r.URL.Query().Get("email")]})
	})
	// The script moves the deadline to show owner rights end with it.
	a.mux.HandleFunc("POST /_fixture/deadline-passed", func(w http.ResponseWriter, r *http.Request) {
		_, err := a.database.Exec(`UPDATE events SET submission_deadline = now() - interval '1 minute' WHERE slug = $1`, slug)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(204)
	})
	stop := make(chan struct{})
	a.mux.HandleFunc("POST /_fixture/stop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
		select {
		case <-stop:
		default:
			close(stop)
		}
	})
	info, _ := json.Marshal(map[string]string{
		"url": a.srv.URL, "site_domain": tsDomain, "event": slug, "team": team, "team_key": key,
		"org_key": org.key, "org_email": org.email, "member_email": member.email,
	})
	if err := os.WriteFile(file, info, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stop:
	case <-time.After(30 * time.Minute):
		t.Fatal("browser fixture timed out")
	}
}
