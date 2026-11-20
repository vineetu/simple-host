package handler

import (
	"context"
	accounts "github.com/vsriram/simple-host/host/access"

	"github.com/vsriram/simple-host/host/model"
	appsites "github.com/vsriram/simple-host/host/sites"
)

// handleSeed is what a first handle is derived from: the account's username
// (its email), except for the instance's own admin row (username "admin",
// which is reserved and would come out as admin-2). The admin gets the first
// label of the instance's domain instead (spring for spring.example.com), or
// "organiser" when that label is reserved or not a handle as written. Taken
// labels fall through to organiser-2 and so on, as for anyone.
func handleSeed(user *model.User, siteDomain string) string {
	return appsites.HandleSeed(user, siteDomain)
}

// assignHandle claims a free URL-safe handle for userID derived from email.
// Failures are logged but never fatal — a missing handle must not break sign-in.
// Owner-intent only: email-code verify, dashboard OAuth, or first deploy.
func assignHandle(ctx context.Context, q accountQuerier, userID, email string, randomHandles bool) {
	accounts.AssignHandle(ctx, q, userID, email, randomHandles)
}
