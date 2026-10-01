package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/email"
)

// Archive cleanup (M4). Once an event is archived, its team sites stay up
// for EventSitesKeep. EventRemovalWarn before they go, the organiser gets
// one email. At the end of the keep window each team's site moves to
// Recently deleted; the event page and the results stay. keep_sites, set by
// the platform admin, leaves the sites up. The sweep runs daily.

const (
	eventCleanupEvery = 24 * time.Hour
	eventCleanupDelay = 10 * time.Minute
)

// SetMailer is the notice sender for the archive warning. Call once at startup.
func (h *HackHandler) SetMailer(m email.Sender) { h.mailer = m }

// StartEventCleanup runs the sweep shortly after boot and then daily.
// EVENTS=hosted only: main starts it from the hosted-mode block.
func (h *HackHandler) StartEventCleanup(ctx context.Context) {
	keep := config.Active().EventSitesKeep
	warn := config.Active().EventRemovalWarn
	log.Printf("event-site cleanup: on (team sites removed %d days after an event archives, one warning %d days before, then daily)", int(keep.Hours()/24), int(warn.Hours()/24))
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(eventCleanupDelay):
		}
		h.runEventCleanup(ctx, time.Now())
		t := time.NewTicker(eventCleanupEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				h.runEventCleanup(ctx, time.Now())
			}
		}
	}()
}

// runEventCleanup is one pass: remove team sites whose keep window has
// ended, then warn organisers whose warning date has arrived and who have
// not been warned. With no mailer that can send a notice, the pass does
// nothing (the same gate as the idle-site cleanup).
func (h *HackHandler) runEventCleanup(ctx context.Context, now time.Time) {
	mailer, ok := h.mailer.(replyNoticeSender)
	if !ok {
		log.Printf("event-site cleanup: no mailer; nothing done")
		return
	}
	keep := config.Active().EventSitesKeep
	warnFor := config.Active().EventRemovalWarn
	events, err := db.ListEventsForSiteCleanup(ctx, h.database)
	if err != nil {
		log.Printf("event-site cleanup: list: %v", err)
		return
	}
	var warn []db.Event
	for _, ev := range events {
		if !ev.ClosedAt.Valid {
			continue
		}
		removeAt := ev.ClosedAt.Time.Add(keep)
		if !now.Before(removeAt) {
			h.removeEventSites(ctx, ev, now, keep)
			continue
		}
		warnAt := removeAt.Add(-warnFor)
		if !now.Before(warnAt) && !ev.RemovalWarnedAt.Valid {
			warn = append(warn, ev)
		}
	}
	for _, ev := range warn {
		h.warnEventRemoval(ctx, mailer, ev, now, ev.ClosedAt.Time.Add(keep), keep, warnFor)
	}
}

// removeEventSites moves every team's site to Recently deleted, then stamps
// sites_removed_at. One team's failure is logged and the rest still move;
// the stamp follows the pass either way, and nothing else on the event changes.
func (h *HackHandler) removeEventSites(ctx context.Context, ev db.Event, now time.Time, keepFor time.Duration) {
	if h.sites == nil {
		log.Printf("event-site cleanup: remove %s: team sites are not connected", ev.Slug)
		return
	}
	teams, err := db.ListEventTeams(ctx, h.database, ev.ID)
	if err != nil {
		log.Printf("event-site cleanup: remove %s: %v", ev.Slug, err)
		return
	}
	for _, team := range teams {
		if err := h.sites.TrashTeamSite(ctx, ev.AccountID, team.Slug); err != nil {
			log.Printf("event-site cleanup: remove %s/%s: %v", ev.Slug, team.Slug, err)
			continue
		}
	}
	tx, err := h.database.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("event-site cleanup: remove %s: %v", ev.Slug, err)
		return
	}
	defer tx.Rollback()
	if err := db.MarkEventSitesRemoved(ctx, tx, ev.ID, now, keepFor); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("event-site cleanup: mark %s removed: %v", ev.Slug, err)
		}
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("event-site cleanup: remove %s: %v", ev.Slug, err)
		return
	}
	log.Printf("event-site cleanup: removed team sites of %s", ev.Slug)
}

// warnEventRemoval marks the warning and emails it in one transaction: the
// mark re-checks that the event still qualifies (under the row lock the
// update takes), the email is sent, and only then is the mark committed. A
// failed send rolls it back; a crash after the send and before the commit
// leaves no warning on record, so the next run sends it again.
func (h *HackHandler) warnEventRemoval(ctx context.Context, mailer replyNoticeSender, ev db.Event, now, removeAt time.Time, keepFor, warnFor time.Duration) {
	if !strings.Contains(ev.ContactEmail, "@") {
		log.Printf("event-site cleanup: warn %s: no contact email", ev.Slug)
		return
	}
	tx, err := h.database.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("event-site cleanup: warn %s: %v", ev.Slug, err)
		return
	}
	defer tx.Rollback()
	if err := db.MarkEventRemovalWarned(ctx, tx, ev.ID, now, keepFor, warnFor); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("event-site cleanup: mark %s warned: %v", ev.Slug, err)
		}
		return
	}
	removeOn := removeAt.UTC().Format("2 January 2006")
	subject := ev.Title + ": team sites will be removed on " + removeOn
	text := fmt.Sprintf(`%s was archived. Its team sites will be removed on %s.

The event page and the results are not affected.

If you need more time, write to support@simple-host.app.

Simple Hack
`, ev.Title, removeOn)
	if err := mailer.SendNoticeReplyTo(ev.ContactEmail, idleReplyTo(), subject, text); err != nil {
		log.Printf("event-site cleanup: warn %s: %v", ev.Slug, err)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("event-site cleanup: warn %s: email sent but not recorded (it goes again next run): %v", ev.Slug, err)
		return
	}
	log.Printf("event-site cleanup: warned organiser of %s", ev.Slug)
}
