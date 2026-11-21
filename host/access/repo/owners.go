package repo

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/model"
)

func ListAllUsers(ctx context.Context, db *sql.DB) ([]model.User, error) {
	return ListUsers(ctx, db)
}
