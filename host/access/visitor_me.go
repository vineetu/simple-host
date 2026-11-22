package access

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/vsriram/simple-host/host/access/repo"
)

// VisitorIdentity is how visitor userID is signed in, for /me: the address
// the site's rules see (VisitorEmail) and the provider of their latest
// sign-in when it was with that address ("" for an emailed code).
func VisitorIdentity(ctx context.Context, database *sql.DB, userID string) (email, provider string, err error) {
	if email, err = VisitorEmail(ctx, database, userID); err != nil {
		return email, "", err
	}
	identity, err := repo.GetLatestOAuthIdentity(ctx, database, userID)
	switch {
	case err == nil && strings.EqualFold(identity.Email.String, email):
		return email, identity.Provider, nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return email, "", err
	}
	return email, "", nil
}
