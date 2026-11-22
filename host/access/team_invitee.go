package access

import (
	"context"
	"database/sql"
	"strings"

	authrules "github.com/vsriram/simple-host/host/access/rules"
	"github.com/vsriram/simple-host/host/model"
)

// InviteeEmail is the one address invitations reach the signed-in person
// at: their sign-in email, when the platform counts it as verified
// (VisitorEmail), in its sign-in form (authrules.NormalEmail: lower case, no
// +tag folding). An account whose username is not an email, or whose email
// is unverified, gets "" and matches no invitation. Invitations follow the
// address, not the account: if a person's sign-in email changes, they see
// those sent to the new address and no longer those sent to the old one.
func InviteeEmail(ctx context.Context, db *sql.DB, user *model.User) (string, error) {
	email, err := VisitorEmail(ctx, db, user.ID)
	if err != nil || !strings.EqualFold(email, user.Username) {
		return "", err
	}
	email, _ = authrules.NormalEmail(email)
	return email, nil
}

// MyInvitations lists the invitations waiting for the signed-in person.
func MyInvitations(ctx context.Context, db *sql.DB, user *model.User) ([]model.TeamInvitation, error) {
	email, err := InviteeEmail(ctx, db, user)
	if err != nil {
		return nil, err
	}
	return InvitationsFor(ctx, db, email)
}
