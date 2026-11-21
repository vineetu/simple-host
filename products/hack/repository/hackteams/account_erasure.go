package hackteams

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

func ErasureEventAccount(ctx context.Context, tx *sql.Tx, id string, out *string) error {
	team, err := GetEventTeamByID(ctx, tx, id)
	if err != nil {
		return err
	}
	ev, err := hackevents.GetEventByID(ctx, tx, team.EventID)
	*out = ev.AccountID
	return err
}
func ErasureMemberCount(ctx context.Context, tx *sql.Tx, id string, out *int) error {
	n, err := CountTeamMembers(ctx, tx, id)
	*out = n
	return err
}
