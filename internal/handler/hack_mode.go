package handler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

// Hosted events (EVENTS=hosted, simple-hack.app; docs/designs/simple-hack-platform.md).
//
// The same binary, run as the hackathon platform, differs from Simple Host in
// a few fixed places, each gated on hackMode:
//
//   - <handle>.<SITE_DOMAIN> answers only for an event's holding account, and
//     only with the server-rendered event page at "/" and a team's gallery
//     screenshot at /screenshots/<team> (serveHackEventHost); every other
//     single-label name is our 404, never the legacy redirect.
//   - Accounts own no personal sites: a site is a team's, of the event's
//     holding account, made only with a team credential (hack_sites.go).
//   - An account's handle is never chosen or changed by its person: it is a
//     random u-<hex> label, so sign-ups cannot squat event names.
//   - Sign-in email says Simple Hack.
var hackMode bool

// SetHackMode turns hosted-events mode on. Call once at startup, before
// serving.
func SetHackMode(on bool) {
	hackMode = on
	baseTextCache.Clear()
}

// HackMode reports whether this instance runs EVENTS=hosted.
func HackMode() bool { return hackMode }

// hackHandle is a new account's handle on the hackathon platform: a random
// label nobody picks, so it can never be an event's name.
func hackHandle() string {
	var b [5]byte
	_, _ = rand.Read(b[:])
	return "u-" + hex.EncodeToString(b[:])
}

// SetHackEventPage sets what renders an event's page on its host: fn writes
// the page and reports true when user is an event's holding account.
func (h *SiteHandler) SetHackEventPage(fn func(w http.ResponseWriter, r *http.Request, user db.User) bool) {
	h.hackEventPage = fn
}

// SetHackScreenshot sets what serves GET/HEAD /screenshots/<team> on an event
// host: fn writes the image and reports true when that team is on the gallery.
func (h *SiteHandler) SetHackScreenshot(fn func(w http.ResponseWriter, r *http.Request, user db.User, team string) bool) {
	h.hackScreenshot = fn
}

// serveHackEventHost answers <event>.<SITE_DOMAIN> in hosted mode. A custom
// event site stays on this origin; the signed-in app and every API stay apex-only.
func (h *SiteHandler) serveHackEventHost(w http.ResponseWriter, r *http.Request, user db.User, api http.Handler) {
	if strings.HasPrefix(r.URL.Path, "/v1/") {
		if h.hackEventAPIAllowed(r, user) {
			api.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found", Code: "not_found"})
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ev, err := db.GetEventByAccount(r.Context(), h.database, user.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderHackNotFound(w, r)
		} else {
			h.renderServiceError(w, r)
		}
		return
	}
	if ev.WebsiteMode == "custom" && !ev.TakenDown() {
		if _, err := db.GetSiteByUser(r.Context(), h.database, ev.AccountID, ev.ID); err == nil {
			h.serveSiteFileRel(w, r, ev.AccountID, ev.ID, r.URL.Path)
			return
		} else if !errors.Is(err, sql.ErrNoRows) {
			h.renderServiceError(w, r)
			return
		}
	}
	if r.URL.Path == "/" && h.hackEventPage != nil && h.hackEventPage(w, r, user) {
		return
	}
	if slug, ok := hackScreenshotPath(r.URL.Path); ok && h.hackScreenshot != nil && h.hackScreenshot(w, r, user, slug) {
		return
	}
	h.renderHackNotFound(w, r)
}

// Only the custom event website's own storage and visitor sign-in endpoints
// are served on its origin. Event management remains on the trusted apex.
func (h *SiteHandler) hackEventAPIAllowed(r *http.Request, owner db.User) bool {
	ev, err := db.GetEventByAccount(r.Context(), h.database, owner.ID)
	if err != nil || ev.WebsiteMode != "custom" || ev.TakenDown() {
		return false
	}
	if _, err := db.GetSiteByUser(r.Context(), h.database, owner.ID, ev.ID); err != nil {
		return false
	}
	p := r.URL.Path
	base := "/v1/sites/" + ev.Slug
	if p == base+"/me" || p == base+"/visitor/auth" || p == base+"/visitor/auth/verify" ||
		strings.HasPrefix(p, base+"/storage/") {
		return true
	}
	return p == "/v1/visitor/establish" || p == "/v1/visitor/logout" ||
		strings.HasPrefix(p, "/v1/visitor/oauth/")
}

// renderHackNotFound is the platform's 404 on an event or unknown host.
func (h *SiteHandler) renderHackNotFound(w http.ResponseWriter, r *http.Request) {
	h.renderNotFoundPage(w, r,
		"There’s nothing here",
		"No event or page lives at this address.",
		h.mainSiteURL(), "Go to "+h.siteDomain)
}

// ---- One name list with the peer instance ---------------------------------
//
// simple-host.app's /v1/events hands out <name>.simple-hack.app records for
// self-hosted events; simple-hack.app hands out <event>.simple-hack.app for
// hosted ones. Each asks the other (EVENT_NAME_PEER, loopback) before taking a
// name: GET /internal/names/taken?zone=<zone>&name=<name> → {"taken": bool}.

// RegisterNamePeer adds GET /internal/names/taken, answered by taken for names
// under any of zones (other zones: not taken). nginx never forwards
// /internal/; the handler also refuses anything but a direct loopback caller.
func RegisterNamePeer(mux *http.ServeMux, zones []string, taken func(ctx context.Context, zone, name string) (bool, error)) {
	ours := map[string]bool{}
	for _, z := range zones {
		if z = strings.ToLower(strings.TrimSpace(z)); z != "" {
			ours[z] = true
		}
	}
	mux.HandleFunc("GET /internal/names/taken", func(w http.ResponseWriter, r *http.Request) {
		if !directLoopback(r) {
			http.NotFound(w, r)
			return
		}
		name := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("name")))
		zone := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("zone")))
		if !ours[zone] || name == "" {
			writeJSON(w, http.StatusOK, map[string]bool{"taken": false})
			return
		}
		t, err := taken(r.Context(), zone, name)
		if err != nil {
			log.Printf("name peer: %s: %v", name, err)
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"taken": t})
	})
}

// directLoopback: the request came straight from this box, not through nginx
// (which always adds X-Forwarded-For / X-Real-IP).
func directLoopback(r *http.Request) bool {
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// NamePeerClient asks the peer at base (http://127.0.0.1:<port>) whether it
// holds name under zone. Any failure is an error: callers refuse the name.
func NamePeerClient(base string) func(ctx context.Context, zone, name string) (bool, error) {
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context, zone, name string) (bool, error) {
		u := base + "/internal/names/taken?zone=" + url.QueryEscape(zone) + "&name=" + url.QueryEscape(name)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return false, err
		}
		res, err := client.Do(req)
		if err != nil {
			return false, err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return false, &peerStatusError{res.StatusCode}
		}
		var out struct {
			Taken *bool `json:"taken"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(nil, res.Body, 4096)).Decode(&out); err != nil || out.Taken == nil {
			return false, &peerStatusError{0}
		}
		return *out.Taken, nil
	}
}

type peerStatusError struct{ status int }

func (e *peerStatusError) Error() string {
	if e.status == 0 {
		return "name peer: unreadable answer"
	}
	return "name peer: status " + http.StatusText(e.status)
}

// DirUsage returns the bytes of regular files under dir, measured at most
// every ten minutes (a walk of the hackathon platform's team sites, for
// HACK_INSTANCE_BUDGET_GB).
func DirUsage(dir string) func(ctx context.Context) (int64, error) {
	var (
		mu     sync.Mutex
		last   time.Time
		cached int64
	)
	return func(ctx context.Context) (int64, error) {
		mu.Lock()
		defer mu.Unlock()
		if !last.IsZero() && time.Since(last) < 10*time.Minute {
			return cached, nil
		}
		var total int64
		err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if d.Type().IsRegular() {
				if info, ierr := d.Info(); ierr == nil {
					total += info.Size()
				}
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
		cached, last = total, time.Now()
		return total, nil
	}
}

// hackAccountDeleteBlock says why an account on the hackathon platform cannot
// be deleted yet ("" when it can). An event's holding account goes only with
// its event (the admin's Events tab). An organiser of an event still running
// ends or deletes it first, so no event is left that nobody can manage (an
// event the platform took down does not hold them). The organiser's name and
// contact address are blanked on the events that stay.
func hackAccountDeleteBlock(ctx context.Context, q db.Querier, userID string) (code, msg string, err error) {
	if !hackMode {
		return "", "", nil
	}
	var running bool
	holding, err := hackHoldingAccount(ctx, q, userID)
	if err != nil {
		return "", "", err
	}
	if holding {
		return "event_account", "this account holds an event; delete the event from the admin Events tab", nil
	}
	if err = q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM event_members m JOIN events e ON e.id = m.event_id
		                WHERE m.user_id = $1 AND m.role = 'organiser' AND e.stage <> 'archived'
		                  AND e.taken_down_at IS NULL)`, userID).Scan(&running); err != nil {
		return "", "", err
	}
	if running {
		return "organises_events", "you organise an event that has not ended; end it, or delete it while nobody has joined, before deleting the account", nil
	}
	// A taken-down event of theirs ends: restoring it later must not bring
	// back a running event nobody can manage.
	if _, err = q.ExecContext(ctx, `
		UPDATE events e SET stage = 'archived', closed_at = COALESCE(e.closed_at, now()), updated_at = now()
		  FROM event_members m
		 WHERE m.event_id = e.id AND m.user_id = $1 AND m.role = 'organiser'
		   AND e.taken_down_at IS NOT NULL AND e.stage <> 'archived'`, userID); err != nil {
		return "", "", err
	}
	if _, err = q.ExecContext(ctx, `
		UPDATE events e SET organiser_name = '', contact_email = '', updated_at = now()
		  FROM event_members m
		 WHERE m.event_id = e.id AND m.user_id = $1 AND m.role = 'organiser'`, userID); err != nil {
		return "", "", err
	}
	// A team this person is alone on goes with them (its code would
	// otherwise stay live on an empty team).
	_, err = q.ExecContext(ctx, `
		DELETE FROM event_teams t USING events e
		 WHERE e.id = t.event_id
		   AND t.id IN (SELECT team_id FROM event_members WHERE user_id = $1 AND team_id IS NOT NULL)
		   AND (SELECT count(*) FROM event_members m WHERE m.team_id = t.id) = 1
		   AND `+db.TeamKeepableSQL, userID)
	return "", "", err
}

// hackHoldingAccount: userID is an event's holding account (never signs in).
func hackHoldingAccount(ctx context.Context, q db.Querier, userID string) (bool, error) {
	if !hackMode {
		return false, nil
	}
	var held bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM events WHERE account_id::text = lower($1))`, userID).Scan(&held)
	return held, err
}
