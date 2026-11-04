package handler

import (
	"context"
	"strings"

	appsites "github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/model"
)

// reservedHandles are path segments / product names that must never be claimed
// as user handles (they collide with API or site routes).
var reservedHandles = map[string]bool{
	"v1":          true,
	"internal":    true,
	"api":         true,
	"auth":        true,
	"me":          true,
	"sites":       true,
	"handles":     true,
	"by-id":       true,
	"static":      true,
	"assets":      true,
	"well-known":  true,
	"favicon.ico": true,
	"robots.txt":  true,
	"admin":       true,
	"root":        true,
	"www":         true,
	"app":         true,
	"dashboard":   true,
	"login":       true,
	"logout":      true,
	"health":      true,
	"healthz":     true,
	"readyz":      true,
	"skills":      true,
	"plugin":      true,
	"install":     true,
	"setup":       true,
	"openapi":     true,
	"llms.txt":    true,
}

// sanitizeHandleBase turns an email into a URL-safe handle base from its local-part:
// lowercase, non-[a-z0-9-] → '-', collapse dashes, trim, truncate to 30 chars.
// Empty result becomes "user".
func sanitizeHandleBase(email string) string { return appsites.SanitizeHandleBase(email) }

// handleSeed is what a first handle is derived from: the account's username
// (its email), except for the instance's own admin row (username "admin",
// which is reserved and would come out as admin-2). The admin gets the first
// label of the instance's domain instead (spring for spring.example.com), or
// "organiser" when that label is reserved or not a handle as written. Taken
// labels fall through to organiser-2 and so on, as for anyone.
func handleSeed(user *model.User, siteDomain string) string {
	if !user.IsAdmin || user.Username != "admin" {
		return user.Username
	}
	label, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(siteDomain)), ".")
	if base := sanitizeHandleBase(label); label != "" && base == label && !appsites.LabelReservedForNew(base) {
		return base
	}
	return "organiser"
}

// assignHandle claims a free URL-safe handle for userID derived from email.
// Failures are logged but never fatal — a missing handle must not break sign-in.
// Owner-intent only: email-code verify, dashboard OAuth, or first deploy.
func assignHandle(ctx context.Context, q accountQuerier, userID, email string, randomHandles bool) {
	appsites.AssignHandle(ctx, q, userID, email, randomHandles)
}
