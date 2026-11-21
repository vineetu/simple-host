package access

import (
	"context"
	"log"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sqlkit"
)

func RecordSignup(ctx context.Context, q sqlkit.Querier, userID string, signup model.Signup) {
	if err := repo.SetSignup(ctx, q, userID, signup); err != nil {
		log.Printf("signup: record source user_id=%s: %v", userID, err)
	}
}
func SignupForClient(ctx context.Context, q sqlkit.Querier, clientID, method string, clean func(string) string) (model.Signup, bool) {
	client, err := repo.GetOAuthClient(ctx, q, clientID)
	if err != nil {
		return model.Signup{}, false
	}
	return model.Signup{Source: model.SignupConnectorPrefix + clean(client.Name), Method: method}, true
}
