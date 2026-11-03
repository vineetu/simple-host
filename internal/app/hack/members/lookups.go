package members

import (
	"context"
	"github.com/vsriram/simple-host/internal/db"
)

// MemberApprovalStatusLookup forwards the MemberApprovalStatus lookup.
func (s *Service) MemberApprovalStatusLookup(ctx context.Context, eventID string, userID string) (string, error) {
	return db.MemberApprovalStatus(ctx, s.db, eventID, userID)
}

// RegistrationSettingsLookup forwards the RegistrationSettings lookup.
func (s *Service) RegistrationSettingsLookup(ctx context.Context, eventID string) ([]db.SignupQuestion, bool, error) {
	return db.RegistrationSettings(ctx, s.db, eventID)
}
