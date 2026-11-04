package accounts

import (
	"context"
	"database/sql"
)

func RenameHandle(ctx context.Context, database *sql.DB, userID, newHandle string) (string, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	old, err := RenameHandleTx(ctx, tx, userID, newHandle)
	if err != nil {
		return "", err
	}
	return old, tx.Commit()
}
