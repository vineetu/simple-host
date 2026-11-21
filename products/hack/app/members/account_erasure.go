package members

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"log"
	"sort"
)

type AccountErasure struct{}

func (AccountErasure) Before(ctx context.Context, tx *sql.Tx, id string) ([]string, error) {
	return hackteams.AdminTeamIDs(ctx, tx, id)
}
func (AccountErasure) After(ctx context.Context, tx *sql.Tx, adminTeamIDs []string) ([]string, error) {
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
			// Not keepable (frozen or pinned).
			if err := hackevents.MarkTeamKeptAfterLeave(ctx, tx, teamID); err != nil {
				return affected, err
			}
			if err := hackteams.RotateTeamCodeTx(ctx, tx, teamID); err != nil {
				return affected, err
			}
			continue
		}
		// Members remain: give them a successor.
		if err := hackteams.EnsureTeamAdmin(ctx, tx, teamID); err != nil {
			return affected, err
		}
	}
	return affected, nil
}

func (AccountErasure) Sweep(ctx context.Context, database *sql.DB, eventAccountIDs []string, trash func(context.Context, string, string) error) {
	for _, accountID := range eventAccountIDs {
		orphans, err := hackteams.OrphanTeamSites(ctx, database, accountID, func(ctx context.Context, id string) ([]model.Site, error) {
			return sites.AccountSites(ctx, database, id)
		})
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

func (AccountErasure) Holds(ctx context.Context, q sqlkit.Querier, id string) (bool, error) {
	return hackevents.AccountHoldsEvent(ctx, q, id)
}

func (AccountErasure) Block(ctx context.Context, tx *sql.Tx, id string, operator bool) (string, string, error) {
	running, err := hackevents.AccountHasRunningEvents(ctx, tx, id)
	if err != nil {
		return "", "", err
	}
	if running {
		msg := "you organise an event that has not ended; end it, or delete it while nobody has joined, before deleting the account"
		if operator {
			msg = "this account organises an event that has not ended; end it or delete it from the Events tab first"
		}
		return "organises_events", msg, nil
	}
	return "", "", hackevents.CleanupDeletedAccountEvents(ctx, tx, id)
}

func (AccountErasure) Rows(ctx context.Context, tx *sql.Tx, id string) error {
	return hackevents.EraseAccountRows(ctx, tx, id)
}

func (AccountErasure) HoldingText(action string) string {
	switch action {
	case "delete":
		return "this account holds an event; delete the event from the admin Events tab"
	case "delete_operator":
		return "this account holds an event; delete the event from the Events tab"
	case "reissue":
		return "this account holds an event and never signs in; manage the event from the admin Events tab"
	case "suspend", "address":
		return "this account holds an event; take the event down or restore it from the admin Events tab"
	case "email":
		return "an event account's name is set by its organiser and cannot be changed"
	}
	return "this account holds sites for a group and cannot be changed here"
}
