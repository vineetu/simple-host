package entries

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/vsriram/simple-host/internal/db"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
	"github.com/vsriram/simple-host/internal/rules/hackentries"
)

// SettleDue runs the deadline sweep for every event: every entry whose team
// is now frozen and not yet settled gets decided -- auto-submitted if complete,
// left as "not submitted" (still judged, with an Incomplete flag) otherwise.
// The per-minute timer calls it alongside PinDueTeams. Stage transitions call
// SettleEventTx in the same transaction so judging never waits for the sweep.
func (s *Service) SettleDue(ctx context.Context) { s.SettleEvent(ctx, "") }

// SettleEvent runs the deadline sweep for an event (or all events if empty).
func (s *Service) SettleEvent(ctx context.Context, eventID string) {
	ids, err := entryrepo.UnsettledFrozenTeamIDs(ctx, s.db, eventID)
	if err != nil {
		log.Printf("hack: settle sweep: %v", err)
		return
	}
	for _, teamID := range ids {
		if err := s.SettleTeam(ctx, teamID); err != nil {
			log.Printf("hack: settle sweep %s: %v", teamID, err)
		}
	}
}

// SettleEventTx settles every unsettled, now-frozen entry of the event in
// the caller's own transaction (the setStage hook: one commit covers the
// stage move and every team's settle). The caller has already locked
// event_teams FOR UPDATE.
//
// Before that, a frozen team that never opened the entry form but has a live
// team site gets an empty entry started, so it is settled (and judged,
// flagged Incomplete) too: the site is part of the entry (full-test fix,
// 2026-11-02). A team with neither an entry nor a live site stays out.
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
	ev, err := db.GetEventByID(ctx, tx, eventID)
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

// SettleTeam locks and settles one team's entry in its own transaction
// (lock order: team, then entry -- there is no specific member or event row
// to lock for this write).
func (s *Service) SettleTeam(ctx context.Context, teamID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE id = $1 FOR UPDATE`, teamID); err != nil {
		return err
	}
	if err := s.SettleTeamTx(ctx, tx, teamID); err != nil {
		return err
	}
	return tx.Commit()
}

// SettleTeamTx is SettleTeam's body, run in a transaction that
// already holds the team row FOR UPDATE.
func (s *Service) SettleTeamTx(ctx context.Context, tx *sql.Tx, teamID string) error {
	st, err := db.TeamWriteStateFor(ctx, tx, teamID, false)
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
	ev, err := db.GetEventByID(ctx, tx, st.EventID)
	if err != nil {
		return err
	}
	fields := hackentries.LoadRules(ev.EntryRules)
	tracks, err := db.ListEventTracks(ctx, tx, ev.ID)
	if err != nil {
		return err
	}
	track, err := db.TeamTrack(ctx, tx, teamID)
	if err != nil {
		return err
	}
	siteLive := s.siteLive(ctx, ev, st.TeamSlug, st.SiteTakenDown)
	check := hackentries.Evaluate(fields, e, siteLive, len(tracks) > 0, track)
	if check.Complete {
		deadline := time.Now()
		if st.Deadline.Valid {
			deadline = st.Deadline.Time
		}
		return entryrepo.SettleEntryComplete(ctx, tx, teamID, deadline)
	}
	return entryrepo.SettleEntryIncomplete(ctx, tx, teamID)
}
