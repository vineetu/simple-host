package members

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"log"
	"sort"
)

type AccountErasure struct{}

func (AccountErasure) AdminTeamIDs(ctx context.Context, tx *sql.Tx, id string) ([]string, error) {
	return hackteams.AdminTeamIDs(ctx, tx, id)
}
func (AccountErasure) FinishTeams(ctx context.Context, tx *sql.Tx, adminTeamIDs []string) ([]string, error) {
	var affected []string
	sort.Strings(adminTeamIDs)
	for _, teamID := range adminTeamIDs {
		var eventAccountID string
		if err := hackteams.ErasureEventAccount(ctx, tx, teamID, &eventAccountID); err != nil {
			if err == sql.ErrNoRows {
				continue // the team is already gone by some other path
			}
			return affected, err
		}
		if err := hackteams.DeleteTeamIfEmpty(ctx, tx, teamID); err != nil {
			return affected, err
		}
		var stillExists bool
		if err := hackteams.ErasureTeamExists(ctx, tx, teamID, &stillExists); err != nil {
			return affected, err
		}
		if !stillExists {
			affected = append(affected, eventAccountID)
			continue
		}
		var members int
		if err := hackteams.ErasureMemberCount(ctx, tx, teamID, &members); err != nil {
			return affected, err
		}
		if members == 0 {
			// Not keepable (frozen or pinned): kept for good and locked, the
			// same as a voluntary last-member leave past the deadline, and
			// its code rotated so the dead link that used to work for this
			// person stops working for anyone else too.
			if err := hackevents.MarkTeamKeptAfterLeave(ctx, tx, teamID); err != nil {
				return affected, err
			}
			if err := hackteams.RotateTeamCodeTx(ctx, tx, teamID); err != nil {
				return affected, err
			}
			continue
		}
		// Members remain: give them a successor. hackteams.EnsureTeamAdmin locks
		// that team's remaining members (sorted), then the team row, so
		// running this sequentially here (never concurrently with itself)
		// carries no extra deadlock risk.
		if err := hackteams.EnsureTeamAdmin(ctx, tx, teamID); err != nil {
			return affected, err
		}
	}
	return affected, nil
}

func (AccountErasure) SweepOrphanSites(ctx context.Context, database *sql.DB, eventAccountIDs []string, trash func(context.Context, string, string) error) {
	for _, accountID := range eventAccountIDs {
		orphans, err := hackteams.OrphanTeamSites(ctx, database, accountID)
		if err != nil {
			log.Printf("hack: orphan sites after account erasure: %v", err)
			continue
		}
		for _, s := range orphans {
			if err := trash(ctx, s.UserID, s.Name); err != nil {
				log.Printf("hack: remove site %s after account erasure: %v", s.Name, err)
			}
		}
	}

}
