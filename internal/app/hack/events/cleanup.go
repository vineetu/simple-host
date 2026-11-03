package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/vsriram/simple-host/internal/db"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/internal/repository/hackteams"
	"log"
	"strings"
	"time"
)

// RunEventCleanup is one pass: remove team sites whose keep window has
// ended, then warn organisers whose warning date has arrived and who have
// not been warned. With no mailer that can send a notice, the pass does
// nothing (the same gate as the idle-site cleanup).
func (s *Service) RunEventCleanup(ctx context.Context, now time.Time, keep, warnFor time.Duration, trash func(context.Context, string, string) error, send func(string, string, string, string) error, replyTo string) {
	if send == nil {
		log.Printf("event-site cleanup: no mailer; nothing done")
		return
	}
	events, err := eventrepo.ListEventsForSiteCleanup(ctx, s.db)
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
			s.removeEventSites(ctx, ev, now, keep, trash)
			continue
		}
		warnAt := removeAt.Add(-warnFor)
		if !now.Before(warnAt) && !ev.RemovalWarnedAt.Valid {
			warn = append(warn, ev)
		}
	}
	for _, ev := range warn {
		s.warnEventRemoval(ctx, send, replyTo, ev, now, ev.ClosedAt.Time.Add(keep), keep, warnFor)
	}
}

// removeEventSites moves every team's site to Recently deleted, then stamps
// sites_removed_at. One team's failure is logged and the rest still move;
// the stamp follows the pass either way, and nothing else on the event changes.
func (s *Service) removeEventSites(ctx context.Context, ev db.Event, now time.Time, keepFor time.Duration, trash func(context.Context, string, string) error) {
	if trash == nil {
		log.Printf("event-site cleanup: remove %s: team sites are not connected", ev.Slug)
		return
	}
	teams, err := teamrepo.ListEventTeams(ctx, s.db, ev.ID)
	if err != nil {
		log.Printf("event-site cleanup: remove %s: %v", ev.Slug, err)
		return
	}
	for _, team := range teams {
		if err := trash(ctx, ev.AccountID, team.Slug); err != nil {
			log.Printf("event-site cleanup: remove %s/%s: %v", ev.Slug, team.Slug, err)
			continue
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("event-site cleanup: remove %s: %v", ev.Slug, err)
		return
	}
	defer tx.Rollback()
	if err := eventrepo.MarkEventSitesRemoved(ctx, tx, ev.ID, now, keepFor); err != nil {
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
func (s *Service) warnEventRemoval(ctx context.Context, send func(string, string, string, string) error, replyTo string, ev db.Event, now, removeAt time.Time, keepFor, warnFor time.Duration) {
	if !strings.Contains(ev.ContactEmail, "@") {
		log.Printf("event-site cleanup: warn %s: no contact email", ev.Slug)
		return
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("event-site cleanup: warn %s: %v", ev.Slug, err)
		return
	}
	defer tx.Rollback()
	if err := eventrepo.MarkEventRemovalWarned(ctx, tx, ev.ID, now, keepFor, warnFor); err != nil {
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
	if err := send(ev.ContactEmail, replyTo, subject, text); err != nil {
		log.Printf("event-site cleanup: warn %s: %v", ev.Slug, err)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("event-site cleanup: warn %s: email sent but not recorded (it goes again next run): %v", ev.Slug, err)
		return
	}
	log.Printf("event-site cleanup: warned organiser of %s", ev.Slug)
}
