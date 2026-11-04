package db

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
)

type ConnectedApp = model.ConnectedApp
type Activation = model.Activation

func ActivationFor(ctx context.Context, q *sql.DB, ids []string) (map[string]*Activation, error) {
	return repo.ActivationFor(ctx, q, ids)
}
