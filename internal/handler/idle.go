package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

// Idle-site cleanup (owner decision 2026-09-27).
//
// A site with no visits by people and no new version for idleAfter gets its
// owner an email with two links that need no sign-in: "Keep it" (resets the
// clock) and "Download it" (the site's export). idleGrace later, with nothing
// done, it moves to Recently deleted (the usual 7-day restore window) and a
// second email carries a one-click "Restore it" link. Exempt: sites with a
// custom domain or a claimed name, sites marked Keep (owner app, PUT
// /v1/sites/{site}/keep, MCP keep_site), admin accounts' sites, taken-down
// sites and suspended accounts, sites their owner took offline, preview sites
// (db/idle.go has the rule).
//
// Off unless IDLE_CLEANUP=on. The admin page's dry run (GET
// /v1/admin/idle-sites) lists what a run would do either way. Each run sends
// at most IDLE_CLEANUP_MAX_EMAILS emails, and a site is only ever removed
// after its warning was sent. Nothing runs while visit data cannot be
// trusted: fewer than idleAfter days of analytics on record, or an ingester
// that has not run for idleIngestStale.
//
// The links carry a random token; only its SHA-256 is stored, and it is
// replaced (or cleared) at every step, so a link works for one site, for one
// warning, and dies when the owner or the cleanup moves on.

const (
	idleAfter        = 90 * 24 * time.Hour
	idleGrace        = 30 * 24 * time.Hour
	idleIngestStale  = 48 * time.Hour
	idleRunEvery     = 6 * time.Hour
	idleDefaultEmail = 50
	idleReplyTo      = "support@simple-host.app"
)

// SetIdleCleanup turns the cleanup on (IDLE_CLEANUP=on) and caps the emails
// one run sends (IDLE_CLEANUP_MAX_EMAILS; <= 0 means the default).
func (h *SiteHandler) SetIdleCleanup(on bool, maxEmails int) {
	h.idleCleanup = on
	if maxEmails <= 0 {
		maxEmails = idleDefaultEmail
	}
	h.idleMaxEmails = maxEmails
}

func (h *SiteHandler) idleEmailCap() int {
	if h.idleMaxEmails <= 0 {
		return idleDefaultEmail
	}
	return h.idleMaxEmails
}

// replyNoticeSender is the part of the mailer that sends a notice people can
// answer (Reply-To support).
type replyNoticeSender interface {
	SendNoticeReplyTo(toEmail, replyTo, subject, text string) error
}

// idleEvidenceOK says whether visit data can be trusted to call sites idle,
// and why not.
func (h *SiteHandler) idleEvidenceOK(ctx context.Context, now time.Time) (bool, string, time.Time, time.Time, error) {
	since, last, err := db.IdleEvidence(ctx, h.database)
	if err != nil {
		return false, "", since, last, err
	}
	switch {
	case since.IsZero() || since.After(now.Add(-idleAfter)):
		return false, "fewer than 90 days of visit records on this server, so nothing can be called idle yet", since, last, nil
	case last.IsZero() || now.Sub(last) > idleIngestStale:
		return false, "visit records are not being read (the analytics ingester has not run for two days), so nothing is called idle", since, last, nil
	}
	return true, "", since, last, nil
}

// StartIdleCleanup runs the cleanup shortly after boot and then every
// idleRunEvery, while IDLE_CLEANUP is on.
func (h *SiteHandler) StartIdleCleanup(ctx context.Context) {
	if !h.idleCleanup {
		return
	}
	log.Printf("idle-site cleanup: on (warn after %d days idle, remove %d days later, at most %d emails per run)", int(idleAfter.Hours()/24), int(idleGrace.Hours()/24), h.idleEmailCap())
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Minute):
		}
		h.runIdleCleanup(ctx, time.Now())
		t := time.NewTicker(idleRunEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				h.runIdleCleanup(ctx, time.Now())
			}
		}
	}()
}

// runIdleCleanup is one pass: drop warnings of sites active again, remove
// sites warned idleGrace ago, warn newly idle sites, within the email cap.
func (h *SiteHandler) runIdleCleanup(ctx context.Context, now time.Time) {
	if !h.idleCleanup {
		return
	}
	mailer, ok := h.mailer.(replyNoticeSender)
	if !ok {
		log.Printf("idle-site cleanup: no mailer; nothing done")
		return
	}
	trusted, why, _, _, err := h.idleEvidenceOK(ctx, now)
	if err != nil {
		log.Printf("idle-site cleanup: %v", err)
		return
	}
	if !trusted {
		log.Printf("idle-site cleanup: skipped: %s", why)
		return
	}
	if n, err := db.ClearStaleIdleWarnings(ctx, h.database); err != nil {
		log.Printf("idle-site cleanup: clear warnings: %v", err)
		return
	} else if n > 0 {
		log.Printf("idle-site cleanup: %d warned site(s) active again", n)
	}
	budget := h.idleEmailCap()

	remove, err := db.ListIdleSitesToRemove(ctx, h.database, now.Add(-idleGrace), budget)
	if err != nil {
		log.Printf("idle-site cleanup: list removals: %v", err)
		return
	}
	for _, s := range remove {
		if budget <= 0 {
			break
		}
		budget--
		h.removeIdleSite(ctx, mailer, s)
	}

	if budget <= 0 {
		log.Printf("idle-site cleanup: email cap reached; the rest wait for the next run")
		return
	}
	warn, err := db.ListIdleSitesToWarn(ctx, h.database, now.Add(-idleAfter), budget)
	if err != nil {
		log.Printf("idle-site cleanup: list warnings: %v", err)
		return
	}
	for _, s := range warn {
		if budget <= 0 {
			break
		}
		budget--
		h.warnIdleSite(ctx, mailer, s, now)
	}
}

// newIdleToken is a fresh link token and the hash stored for it.
func newIdleToken() (string, []byte) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic("idle links: no randomness: " + err.Error())
	}
	tok := hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(tok))
	return tok, sum[:]
}

func idleTokenHash(tok string) []byte {
	sum := sha256.Sum256([]byte(tok))
	return sum[:]
}

func (h *SiteHandler) idleLink(action, tok string) string {
	return h.exportLinkBase() + "/v1/idle/" + action + "?t=" + url.QueryEscape(tok)
}

func (h *SiteHandler) idleSiteAddress(s db.IdleSite) string {
	if u := h.SiteURL(s.OwnerHandle, s.Name); u != "" {
		return u
	}
	return s.Name
}

// warnIdleSite records the warning, then emails it; a failed send takes the
// warning back, so the removal clock starts only for owners who were told.
func (h *SiteHandler) warnIdleSite(ctx context.Context, mailer replyNoticeSender, s db.IdleSite, now time.Time) {
	if !strings.Contains(s.OwnerEmail, "@") {
		return
	}
	tok, hash := newIdleToken()
	if err := db.MarkIdleWarned(ctx, h.database, s.SiteID, hash); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("idle-site cleanup: mark %s warned: %v", s.SiteID, err)
		}
		return
	}
	days := int(now.Sub(s.LastActivity).Hours() / 24)
	removeOn := now.Add(idleGrace).UTC().Format("2 January 2006")
	subject := "Your site " + s.Name + " has had no visitors for " + fmt.Sprint(days) + " days"
	text := fmt.Sprintf(`Your Simple Host site %s (%s) has had no visitors and no new versions for %d days.

Keep it online (one click, no sign-in):
%s

Download a copy of its files and saved data:
%s

If you do nothing, on %s it moves to Recently deleted, where it can still be restored for 7 days before it is removed for good. A site stays up for good if you mark it Keep on your Simple Host page, or give it its own domain or name.

Questions? Just reply to this email.

Simple Host
`, s.Name, h.idleSiteAddress(s), days, h.idleLink("keep", tok), h.idleLink("download", tok), removeOn)
	if err := mailer.SendNoticeReplyTo(s.OwnerEmail, idleReplyTo, subject, text); err != nil {
		log.Printf("idle-site cleanup: warn %s: %v", s.SiteID, err)
		if _, err := h.database.ExecContext(ctx, `UPDATE sites SET idle_warned_at = NULL, idle_token_hash = NULL WHERE id = $1`, s.SiteID); err != nil {
			log.Printf("idle-site cleanup: undo warning %s: %v", s.SiteID, err)
		}
		return
	}
	log.Printf("idle-site cleanup: warned owner of %s (%s)", s.Name, s.SiteID)
}

// removeIdleSite moves a site to Recently deleted and emails the restore link.
func (h *SiteHandler) removeIdleSite(ctx context.Context, mailer replyNoticeSender, s db.IdleSite) {
	unlock := h.lockSite(s.UserID, s.Name)
	defer unlock()
	site, err := db.GetSiteByUser(ctx, h.database, s.UserID, s.Name)
	if err != nil || site.ID != s.SiteID || site.Suspended() || site.Offline {
		return
	}
	tok, hash := newIdleToken()
	err = h.trashSite(ctx, site, func(tx *sql.Tx) error {
		return db.MarkIdleRemoved(ctx, tx, site.ID, hash)
	})
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("idle-site cleanup: remove %s: %v", s.SiteID, err)
		}
		return
	}
	log.Printf("idle-site cleanup: moved %s (%s) to Recently deleted", s.Name, s.SiteID)
	if !strings.Contains(s.OwnerEmail, "@") {
		return
	}
	purge := time.Now().Add(db.DeletedSiteRetention).UTC().Format("2 January 2006")
	subject := "Your site " + s.Name + " was moved to Recently deleted"
	text := fmt.Sprintf(`Your Simple Host site %s had no visitors for over 120 days, and nobody chose to keep it after our email a month ago, so it was moved to Recently deleted.

Restore it exactly as it was (one click, no sign-in), until %s:
%s

After that it is removed for good. If you meant to let it go, there is nothing to do.

Questions? Just reply to this email.

Simple Host
`, s.Name, purge, h.idleLink("restore", tok))
	if err := mailer.SendNoticeReplyTo(s.OwnerEmail, idleReplyTo, subject, text); err != nil {
		log.Printf("idle-site cleanup: removal notice %s: %v", s.SiteID, err)
	}
}

// idleLinkSite resolves a link token to its site, or renders why not.
func (h *SiteHandler) idleLinkSite(w http.ResponseWriter, r *http.Request) (db.IdleLink, bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	tok := strings.TrimSpace(r.URL.Query().Get("t"))
	if len(tok) != 48 {
		h.idleLinkGone(w, r)
		return db.IdleLink{}, false
	}
	l, err := db.GetIdleLink(r.Context(), h.database, idleTokenHash(tok))
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			h.renderServiceError(w)
			return db.IdleLink{}, false
		}
		h.idleLinkGone(w, r)
		return db.IdleLink{}, false
	}
	return l, true
}

func (h *SiteHandler) idleLinkGone(w http.ResponseWriter, r *http.Request) {
	h.renderMessagePage(w, r, http.StatusNotFound, "This link has already been used or has expired",
		"Sign in to Simple Host to see your sites, keep them, or restore one from Recently deleted.",
		h.exportLinkBase()+"/", "Go to Simple Host")
}

// idleKeep GET /v1/idle/keep?t= — "Keep it": the site stays, its clock resets.
func (h *SiteHandler) idleKeep(w http.ResponseWriter, r *http.Request) {
	l, ok := h.idleLinkSite(w, r)
	if !ok {
		return
	}
	if l.Deleted {
		h.idleLinkGone(w, r)
		return
	}
	if err := db.KeepIdleSite(r.Context(), h.database, l.SiteID); err != nil {
		h.renderServiceError(w)
		return
	}
	h.renderMessagePage(w, r, http.StatusOK, "Kept: "+html.EscapeString(l.Name)+" stays online",
		"Nothing else to do. We will only ask again if it goes another 90 days without visitors or a new version.",
		h.exportLinkBase()+"/", "Go to Simple Host")
}

// idleDownload GET /v1/idle/download?t= — the site's export, while warned.
func (h *SiteHandler) idleDownload(w http.ResponseWriter, r *http.Request) {
	l, ok := h.idleLinkSite(w, r)
	if !ok {
		return
	}
	site, err := db.GetSiteByID(r.Context(), h.database, l.SiteID)
	if err != nil || site.Deleted || site.UserID != l.UserID {
		h.idleLinkGone(w, r)
		return
	}
	if site.OwnerSuspended {
		writeAccountSuspended(w)
		return
	}
	h.writeExport(w, r, site)
}

// idleRestore GET /v1/idle/restore?t= — brings a site the cleanup removed
// back from Recently deleted, exactly as it was, with a fresh clock.
func (h *SiteHandler) idleRestore(w http.ResponseWriter, r *http.Request) {
	l, ok := h.idleLinkSite(w, r)
	if !ok {
		return
	}
	if !l.Deleted || !l.Removed {
		h.idleLinkGone(w, r)
		return
	}
	unlock := h.lockSite(l.UserID, l.Name)
	defer unlock()
	d, err := db.GetDeletedSiteByUser(r.Context(), h.database, l.UserID, l.Name)
	if err != nil || d.ID != l.SiteID {
		h.idleLinkGone(w, r)
		return
	}
	owner, err := db.GetUserByID(r.Context(), h.database, l.UserID)
	if err != nil {
		h.renderServiceError(w)
		return
	}
	site, err := h.restoreTrashedSite(r.Context(), d, owner.Handle.String, func(tx *sql.Tx) error {
		return db.KeepIdleSite(r.Context(), tx, d.ID)
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.idleLinkGone(w, r)
			return
		}
		log.Printf("idle-site restore %s: %v", l.SiteID, err)
		h.renderServiceError(w)
		return
	}
	addr := h.siteURLFor(site)
	h.renderMessagePage(w, r, http.StatusOK, "Restored: "+html.EscapeString(site.Name)+" is back online",
		"It is back exactly as it was, with all its versions and saved data.",
		addr, "Open "+html.EscapeString(site.Name))
}

// setSiteKeep PUT /v1/sites/{sitename}/keep {"keep": bool} — the Keep flag.
func (h *SiteHandler) setSiteKeep(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	var req struct {
		Keep *bool `json:"keep"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Keep == nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `body must be {"keep": true} or {"keep": false}`})
		return
	}
	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if refuseSuspendedSite(w, site) {
		return
	}
	if err := db.SetSiteKeep(r.Context(), h.database, site.ID, *req.Keep); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": site.Name, "keep": *req.Keep})
}

type idleSiteRow struct {
	Site         string     `json:"site"`
	Owner        string     `json:"owner"`
	LastActivity time.Time  `json:"last_activity"`
	WarnedAt     *time.Time `json:"warned_at,omitempty"`
}

// adminIdleSites GET /v1/admin/idle-sites — the dry run: which sites a run
// would warn and which it would move to Recently deleted, now, whether or not
// the cleanup is on.
func (h *SiteHandler) adminIdleSites(w http.ResponseWriter, r *http.Request) {
	if !accountAdmin(w, r) {
		return
	}
	now := time.Now()
	trusted, why, since, last, err := h.idleEvidenceOK(r.Context(), now)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	rows := func(list []db.IdleSite) []idleSiteRow {
		out := make([]idleSiteRow, 0, len(list))
		for _, s := range list {
			row := idleSiteRow{Site: s.Name, Owner: s.OwnerEmail, LastActivity: s.LastActivity.UTC()}
			if s.WarnedAt.Valid {
				t := s.WarnedAt.Time.UTC()
				row.WarnedAt = &t
			}
			out = append(out, row)
		}
		return out
	}
	const show = 500
	warn, err := db.ListIdleSitesToWarn(r.Context(), h.database, now.Add(-idleAfter), show)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	remove, err := db.ListIdleSitesToRemove(r.Context(), h.database, now.Add(-idleGrace), show)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	resp := map[string]any{
		"enabled":            h.idleCleanup,
		"max_emails_per_run": h.idleEmailCap(),
		"idle_days":          int(idleAfter.Hours() / 24),
		"grace_days":         int(idleGrace.Hours() / 24),
		"visit_data_ok":      trusted,
		"would_warn":         rows(warn),
		"would_remove":       rows(remove),
	}
	if !trusted {
		resp["visit_data_note"] = why
	}
	if !since.IsZero() {
		resp["visits_recorded_since"] = since.UTC()
	}
	if !last.IsZero() {
		resp["visits_last_read"] = last.UTC()
	}
	writeJSON(w, http.StatusOK, resp)
}
