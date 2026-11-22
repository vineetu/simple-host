package data

import (
	"context"
	"database/sql"

	repository "github.com/vsriram/simple-host/host/data/repo"
	"github.com/vsriram/simple-host/host/model"
)

// ChangesElsewhere is the changes this person made, signed in, to saved data
// on sites that are not theirs, oldest first (Download my data).
func ChangesElsewhere(ctx context.Context, db *sql.DB, userID string) ([]model.ChangeElsewhere, error) {
	return repository.ListChangesElsewhere(ctx, db, userID)
}
