package accounts

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/model"
)

func ListAllUsers(ctx context.Context, db *sql.DB) ([]model.User, error) {
	return ListUsers(ctx, db, false)
}
func ListSiteOwners(ctx context.Context, db *sql.DB) ([]model.User, error) {
	return ListUsers(ctx, db, true)
}
