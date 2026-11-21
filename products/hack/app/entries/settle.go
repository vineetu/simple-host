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
	for _, u := range ids {
		teamID := u.TeamID
		if err := s.SettleTeam(ctx, u.EventID, teamID, func() {
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
	for _, u := range ids {
		if err := s.SettleTeamTx(ctx, tx, eventID, u.TeamID); err != nil {
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

func (s *Service) SettleTeam(ctx context.Context, eventID, teamID string, progress ...func()) error {
	changed := false
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := teamrepo.LockTeamRow(ctx, tx, teamID); err != nil {
		return err
	}
	if err := s.SettleTeamTx(ctx, tx, eventID, teamID, func() { changed = true }); err != nil {
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
func (s *Service) SettleTeamTx(ctx context.Context, tx *sql.Tx, eventID, teamID string, progress ...func()) error {
	st, err := teamrepo.TeamWriteStateFor(ctx, tx, eventID, teamID, false)
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

type SweepReport interface {
	Progress()
	Fail(string, error)
}
