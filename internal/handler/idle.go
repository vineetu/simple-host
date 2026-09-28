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
	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
)

// Idle-site cleanup (owner decision 2026-09-27).
//
// A site with no visits by people and no new version for idleAfter gets its
// owner an email with a "Keep it" link that needs no sign-in (it resets the
// clock) and a link to their Simple Host page, where they can download it
// after signing in. idleGrace later, with nothing done, it moves to Recently
// deleted (the usual 7-day restore window) and a second email carries a
// "Restore it" link. Exempt: sites with a custom domain or a claimed name,
// sites marked Keep (owner app, PUT /v1/sites/{site}/keep, MCP keep_site),
// admin, reviewer and event accounts' sites, handles in
// IDLE_CLEANUP_EXEMPT_HANDLES, taken-down sites and suspended accounts, sites
// their owner took offline, preview sites (db/idle.go has the rule).
// Activity is a person's visit, a new version, a saved-data or list write, a
// restore or "Keep it".
//
// Off unless IDLE_CLEANUP=on. The admin page's dry run (GET
// /v1/admin/idle-sites) lists what a run would do either way. Each run sends
// at most IDLE_CLEANUP_MAX_EMAILS emails. A warning is only on record once
// its email was accepted (the mark and the send share one transaction), and a
// removal re-checks every condition in the transaction that moves the site,
// so a Keep, deploy or domain that lands mid-run wins. Nothing runs while
// visit data cannot be trusted: fewer than idleAfter days of analytics on
// record, or an ingester that has not run for idleIngestStale.
//
// The emailed links open a confirmation page (GET) and act only when the
// person presses its button (POST), so mail scanners that follow every link
// change nothing. They carry a random token; only its SHA-256 is stored, and
// it is replaced (or cleared) at every step, so a link works for one site,
// for one warning, and dies when the owner or the cleanup moves on, when the
// account's sign-in email changes, or while the site is taken down.

const (
	idleIngestStale  = 6 * time.Hour
	idleRunEvery     = 6 * time.Hour
	idleDefaultEmail = 50
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

// SetIdleExempt sets the accounts the cleanup never touches besides the
// built-in rules: handles (IDLE_CLEANUP_EXEMPT_HANDLES) and the plugin
// reviewer account (REVIEW_ACCOUNT_EMAIL).
func (h *SiteHandler) SetIdleExempt(handles []string, reviewerEmail string) {
	ex := db.IdleExempt{ReviewerEmail: strings.TrimSpace(reviewerEmail)}
	for _, hd := range handles {
		if hd = strings.ToLower(strings.TrimSpace(hd)); hd != "" {
			ex.Handles = append(ex.Handles, hd)
		}
	}
	h.idleExempt = ex
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
	case since.IsZero() || since.After(now.Add(-idleAfter())):
		return false, "fewer than " + config.Span(idleAfter()) + " of visit records on this server, so nothing can be called idle yet", since, last, nil
	case last.IsZero() || now.Sub(last) > idleIngestStale:
		return false, "visit records are not being read (the analytics ingester has not run for six hours), so nothing is called idle", since, last, nil
	}
	return true, "", since, last, nil
}

// StartIdleCleanup runs the cleanup shortly after boot and then every
// idleRunEvery, while IDLE_CLEANUP is on.
func (h *SiteHandler) StartIdleCleanup(ctx context.Context) {
	if !h.idleCleanup {
		return
	}
	log.Printf("idle-site cleanup: on (warn after %d days idle, remove %d days later, at most %d emails per run)", int(idleAfter().Hours()/24), int(idleGrace().Hours()/24), h.idleEmailCap())
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
	if n, err := db.ClearStaleIdleWarnings(ctx, h.database, h.idleExempt); err != nil {
		log.Printf("idle-site cleanup: clear warnings: %v", err)
		return
	} else if n > 0 {
		log.Printf("idle-site cleanup: %d warned site(s) active again", n)
	}
	budget := h.idleEmailCap()

	remove, err := db.ListIdleSitesToRemove(ctx, h.database, h.idleExempt, now, idleGrace(), budget)
	if err != nil {
		log.Printf("idle-site cleanup: list removals: %v", err)
		return
	}
	for _, s := range remove {
		if budget <= 0 {
			break
		}
		budget--
		h.removeIdleSite(ctx, mailer, s, now)
	}

	if budget <= 0 {
		log.Printf("idle-site cleanup: email cap reached; the rest wait for the next run")
		return
	}
	warn, err := db.ListIdleSitesToWarn(ctx, h.database, h.idleExempt, now.Add(-idleAfter()), budget)
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

// idleLink carries the token in the fragment (fragmentLinkGET), so it never
// reaches a server log.
func (h *SiteHandler) idleLink(action, tok string) string {
	return h.exportLinkBase() + "/v1/idle/" + action + "#t=" + url.QueryEscape(tok)
}

func (h *SiteHandler) idleSiteAddress(s db.IdleSite) string {
	if u := h.SiteURL(s.OwnerHandle, s.Name); u != "" {
		return u
	}
	return s.Name
}

// idleOwnerApp is where the owner signs in to see, keep or download the site.
func (h *SiteHandler) idleOwnerApp(s db.IdleSite) string {
	if s.OwnerHandle != "" {
		return h.mainSiteURL() + "/" + s.OwnerHandle
	}
	return h.exportLinkBase() + "/"
}

// warnIdleSite marks the warning and emails it in one transaction: the mark
// re-checks that the site still qualifies (under its row lock), the email is
// sent, and only then is the mark committed. A failed send rolls it back; a
// crash after the send and before the commit leaves no warning on record, so
// the next run sends it again (a repeat email, never a removal nobody was
// told about).
func (h *SiteHandler) warnIdleSite(ctx context.Context, mailer replyNoticeSender, s db.IdleSite, now time.Time) {
	if !strings.Contains(s.OwnerEmail, "@") {
		return
	}
	tok, hash := newIdleToken()
	tx, err := h.database.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("idle-site cleanup: warn %s: %v", s.SiteID, err)
		return
	}
	defer tx.Rollback()
	removeAt := now.Add(idleGrace())
	if err := db.MarkIdleWarned(ctx, tx, h.idleExempt, s.SiteID, hash, now.Add(-idleAfter()), removeAt); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("idle-site cleanup: mark %s warned: %v", s.SiteID, err)
		}
		return
	}
	days := int(now.Sub(s.LastActivity).Hours() / 24)
	removeOn := removeAt.UTC().Format("2 January 2006")
	subject := "Your site " + s.Name + " has had no visitors for " + fmt.Sprint(days) + " days"
	text := fmt.Sprintf(`Your Simple Host site %s (%s) has had no visitors, no new versions and no saved data for %d days.

Keep it online (opens a page with a Keep button, no sign-in):
%s

To download a copy of its files and saved data, sign in on your Simple Host page:
%s

If you do nothing, on %s it moves to Recently deleted, where it can still be restored for %s before it is removed for good. A site stays up for good if you mark it Keep on your Simple Host page, or give it its own domain or name.

Questions? Just reply to this email.

Simple Host
`, s.Name, h.idleSiteAddress(s), days, h.idleLink("keep", tok), h.idleOwnerApp(s), removeOn, config.Span(db.DeletedSiteRetention()))
	if err := mailer.SendNoticeReplyTo(s.OwnerEmail, idleReplyTo(), subject, text); err != nil {
		log.Printf("idle-site cleanup: warn %s: %v", s.SiteID, err)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("idle-site cleanup: warn %s: email sent but not recorded (it goes again next run): %v", s.SiteID, err)
		return
	}
	log.Printf("idle-site cleanup: warned owner of %s (%s)", s.Name, s.SiteID)
}

// removeIdleSite moves a site to Recently deleted and emails the restore
// link, in the transaction that moves it: the removal re-checks that the site
// still qualifies (a Keep, deploy, save or new domain since the list was read
// rolls it back), the email goes out before the files move and the commit,
// and a failed send leaves the site where it is for the next run.
func (h *SiteHandler) removeIdleSite(ctx context.Context, mailer replyNoticeSender, s db.IdleSite, now time.Time) {
	if !strings.Contains(s.OwnerEmail, "@") {
		return
	}
	unlock := h.lockSite(s.UserID, s.Name)
	defer unlock()
	site, err := db.GetSiteByUser(ctx, h.database, s.UserID, s.Name)
	if err != nil || site.ID != s.SiteID || site.Suspended() || site.Offline {
		return
	}
	tok, hash := newIdleToken()
	purge := time.Now().Add(db.DeletedSiteRetention()).UTC().Format("2 January 2006")
	// How long ago the warning went, as it was (the grace in force then may
	// differ from today's).
	warnedAgo := config.Span(idleGrace())
	if s.WarnedAt.Valid {
		if d := int(now.Sub(s.WarnedAt.Time) / (24 * time.Hour)); d >= 1 {
			warnedAgo = config.Count(d, "day")
		}
	}
	subject := "Your site " + s.Name + " was moved to Recently deleted"
	text := fmt.Sprintf(`Your Simple Host site %s had no visitors for over %s, and nobody chose to keep it after our email %s ago, so it was moved to Recently deleted.

Restore it exactly as it was (opens a page with a Restore button, no sign-in), until %s:
%s

You can also restore or download it after signing in on your Simple Host page:
%s

After that it is removed for good. If you meant to let it go, there is nothing to do.

Questions? Just reply to this email.

Simple Host
`, s.Name, config.Span(idleAfter()+idleGrace()), warnedAgo, purge, h.idleLink("restore", tok), h.idleOwnerApp(s))
	err = h.trashSite(ctx, site, func(tx *sql.Tx) error {
		if err := db.MarkIdleRemoved(ctx, tx, h.idleExempt, site.ID, hash, now, idleGrace()); err != nil {
			return err
		}
		return mailer.SendNoticeReplyTo(s.OwnerEmail, idleReplyTo(), subject, text)
	})
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("idle-site cleanup: remove %s: %v", s.SiteID, err)
		}
		return
	}
	log.Printf("idle-site cleanup: moved %s (%s) to Recently deleted", s.Name, s.SiteID)
}

// idleLinkSite resolves a link token to its site, or renders why not.
func (h *SiteHandler) idleLinkSite(w http.ResponseWriter, r *http.Request) (db.IdleLink, bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if fragmentLinkGET(w, r, h.chromeBase(r), r.URL.Path) {
		return db.IdleLink{}, false
	}
	tok := idleLinkToken(w, r)
	if len(tok) != 48 {
		h.idleLinkGone(w, r)
		return db.IdleLink{}, false
	}
	l, err := db.GetIdleLink(r.Context(), h.database, idleTokenHash(tok))
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			h.renderServiceError(w, r)
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

// idleLinkToken is the link's token: from the query on GET (older links),
// from the posted form on POST (linkToken).
func idleLinkToken(w http.ResponseWriter, r *http.Request) string { return linkToken(w, r) }

// idleLinkUsable refuses a link whose site is taken down (or whose owner's
// account is suspended): it then does nothing until the operator lifts it.
func (h *SiteHandler) idleLinkUsable(w http.ResponseWriter, r *http.Request, l db.IdleLink) bool {
	if l.TakenDown {
		h.renderMessagePage(w, r, http.StatusForbidden, "This site is taken down",
			supportText("The link does nothing while the site is taken down. Write to support@simple-host.app if you think this is a mistake."),
			h.exportLinkBase()+"/", "Go to Simple Host")
		return false
	}
	return true
}

// idleKeep GET /v1/idle/keep?t= shows "Keep it" with a button; POST (the
// button, t in the form) keeps the site: it stays, its clock resets.
func (h *SiteHandler) idleKeep(w http.ResponseWriter, r *http.Request) {
	l, ok := h.idleLinkSite(w, r)
	if !ok {
		return
	}
	if l.Deleted {
		h.idleLinkGone(w, r)
		return
	}
	if !h.idleLinkUsable(w, r, l) {
		return
	}
	if linkConfirming(r) {
		writeMessagePage(w, r, h.chromeBase(r), http.StatusOK, "Keep "+html.EscapeString(l.Name)+" online?",
			"It stays up, and we only ask again if it goes another "+config.Span(idleAfter())+" without visitors, a new version or saved data.",
			"", "", confirmForm("/v1/idle/keep", map[string]string{"t": idleLinkToken(w, r)}, "Keep it online"))
		return
	}
	if err := db.KeepIdleSite(r.Context(), h.database, l.SiteID); err != nil {
		h.renderServiceError(w, r)
		return
	}
	h.renderMessagePage(w, r, http.StatusOK, "Kept: "+html.EscapeString(l.Name)+" stays online",
		"Nothing else to do. We will only ask again if it goes another "+config.Span(idleAfter())+" without visitors, a new version or saved data.",
		h.exportLinkBase()+"/", "Go to Simple Host")
}

// idleRestore GET /v1/idle/restore?t= shows "Restore it" with a button; POST
// brings a site the cleanup removed back from Recently deleted, exactly as it
// was, with a fresh clock.
func (h *SiteHandler) idleRestore(w http.ResponseWriter, r *http.Request) {
	l, ok := h.idleLinkSite(w, r)
	if !ok {
		return
	}
	if !l.Deleted || !l.Removed {
		h.idleLinkGone(w, r)
		return
	}
	if !h.idleLinkUsable(w, r, l) {
		return
	}
	if linkConfirming(r) {
		writeMessagePage(w, r, h.chromeBase(r), http.StatusOK, "Restore "+html.EscapeString(l.Name)+"?",
			"It comes back exactly as it was, with all its versions and saved data.",
			"", "", confirmForm("/v1/idle/restore", map[string]string{"t": idleLinkToken(w, r)}, "Restore it"))
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
		h.renderServiceError(w, r)
		return
	}
	// RestoreDeletedSite ends the link and restarts the idle clock.
	site, err := h.restoreTrashedSite(r.Context(), d, owner.Handle.String, nil)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.idleLinkGone(w, r)
			return
		}
		log.Printf("idle-site restore %s: %v", l.SiteID, err)
		h.renderServiceError(w, r)
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
	warn, err := db.ListIdleSitesToWarn(r.Context(), h.database, h.idleExempt, now.Add(-idleAfter()), show)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	remove, err := db.ListIdleSitesToRemove(r.Context(), h.database, h.idleExempt, now, idleGrace(), show)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	resp := map[string]any{
		"enabled":            h.idleCleanup,
		"max_emails_per_run": h.idleEmailCap(),
		"idle_days":          int(idleAfter().Hours() / 24),
		"grace_days":         int(idleGrace().Hours() / 24),
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
