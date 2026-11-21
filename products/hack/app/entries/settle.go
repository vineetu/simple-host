package entries

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// SettleDue settles every frozen, unsettled entry: complete ones auto-submit, the rest stay not submitted.
func (s *Service) SettleDue(ctx context.Context, reports ...SweepReport) {
	s.SettleEvent(ctx, "", reports...)
}

// SettleEvent runs the deadline sweep for an event (or all events if empty).
func (s *Service) SettleEvent(ctx context.Context, eventID string, reports ...SweepReport) {
	ids, err := entryrepo.UnsettledFrozenTeamIDs(ctx, s.db, eventID)
	if err != nil {
		log.Printf("hack: settle sweep: %v", err)
		for _, r := range reports {
			r.Fail("list unsettled entries", err)
		}
		return
	}
	for _, teamID := range ids {
		if err := s.SettleTeam(ctx, teamID, func() {
			for _, r := range reports {
				r.Progress()
			}
		}); err != nil {
			log.Printf("hack: settle sweep %s: %v", teamID, err)
			for _, r := range reports {
				r.Fail("settle entry", err)
			}
		}
	}
}

// SettleEventTx settles the event's frozen entries in the caller's tx, which holds event_teams FOR UPDATE.
// A frozen team with a live site but no entry gets an empty one first, so it is judged too.
func (s *Service) SettleEventTx(ctx context.Context, tx *sql.Tx, eventID string) error {
	if err := s.startSiteOnlyEntries(ctx, tx, eventID); err != nil {
		return err
	}
	ids, err := entryrepo.UnsettledFrozenTeamIDs(ctx, tx, eventID)
	if err != nil {
		return err
	}
	for _, teamID := range ids {
		if err := s.SettleTeamTx(ctx, tx, teamID); err != nil {
			return err
		}
	}
	return nil
}

// startSiteOnlyEntries starts an empty entry row for each frozen team of the
// event that has no entry but a live team site (SettleEventTx's first step).
func (s *Service) startSiteOnlyEntries(ctx context.Context, tx *sql.Tx, eventID string) error {
	teams, err := entryrepo.FrozenTeamsWithoutEntry(ctx, tx, eventID)
	if err != nil || len(teams) == 0 {
		return err
	}
	ev, err := eventrepo.GetEventByID(ctx, tx, eventID)
	if err != nil {
		return err
	}
	for _, t := range teams {
		if !s.siteLive(ctx, ev, t.Slug, t.SiteTakenDown) {
			continue
		}
		if err := entryrepo.StartEmptyEventEntry(ctx, tx, eventID, t.TeamID); err != nil {
			return err
		}
	}
	return nil
}

// SettleTeam locks the team, then its entry, and settles it in its own transaction.
func (s *Service) SettleTeam(ctx context.Context, teamID string, progress ...func()) error {
	changed := false
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE id = $1 FOR UPDATE`, teamID); err != nil {
		return err
	}
	if err := s.SettleTeamTx(ctx, tx, teamID, func() { changed = true }); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if changed {
		for _, fn := range progress {
			fn()
		}
	}
	return nil
}

// SettleTeamTx settles one entry in a tx that already holds the team row FOR UPDATE.
func (s *Service) SettleTeamTx(ctx context.Context, tx *sql.Tx, teamID string, progress ...func()) error {
	st, err := teamrepo.TeamWriteStateFor(ctx, tx, teamID, false)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !st.Frozen() {
		return nil // raced with an extension since the candidate list was read
	}
	e, err := entryrepo.GetEventEntryForUpdate(ctx, tx, teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // no entry row: "Not started", nothing to settle
	}
	if err != nil {
		return err
	}
	if e.SubmittedAt.Valid || e.SubmitSettledAt.Valid {
		return nil // already decided
	}
	ev, err := eventrepo.GetEventByID(ctx, tx, st.EventID)
	if err != nil {
		return err
	}
	check, err := s.EvaluateEntryTx(ctx, tx, ev, st, teamID, e)
	if err != nil {
		return err
	}
	if check.Complete {
		at := time.Now()
		if st.Deadline.Valid {
			at = st.Deadline.Time
		}
		err = entryrepo.SettleEntryComplete(ctx, tx, teamID, at)
	} else {
		err = entryrepo.SettleEntryIncomplete(ctx, tx, teamID)
	}
	if err != nil {
		return err
	}
	for _, fn := range progress {
		fn()
	}
	return nil
}

// SweepReport receives committed entry settlement outcomes.
type SweepReport interface {
	Progress()
	Fail(string, error)
}
