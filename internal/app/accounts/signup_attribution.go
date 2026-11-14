package accounts

import (
	"context"
	"database/sql"
	"log"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
)

// AttributionStore may be the sign-in transaction; recording never opens a nested transaction.
type AttributionStore interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func RecordSignup(ctx context.Context, q AttributionStore, userID string, signup model.Signup) {
	if err := repo.SetSignup(ctx, q, userID, signup); err != nil {
		log.Printf("signup: record source user_id=%s: %v", userID, err)
	}
}
func SignupForClient(ctx context.Context, q AttributionStore, clientID, method string, clean func(string) string) (model.Signup, bool) {
	client, err := repo.GetOAuthClient(ctx, q, clientID)
	if err != nil {
		return model.Signup{}, false
	}
	return model.Signup{Source: model.SignupConnectorPrefix + clean(client.Name), Method: method}, true
}
