package members

import (
	"context"

	model "github.com/vsriram/simple-host/internal/model"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
)

// MemberApprovalStatusLookup forwards the MemberApprovalStatus lookup.
func (s *Service) MemberApprovalStatusLookup(ctx context.Context, eventID string, userID string) (string, error) {
	return eventrepo.MemberApprovalStatus(ctx, s.db, eventID, userID)
}

// RegistrationSettingsLookup forwards the RegistrationSettings lookup.
func (s *Service) RegistrationSettingsLookup(ctx context.Context, eventID string) ([]model.SignupQuestion, bool, error) {
	return eventrepo.RegistrationSettings(ctx, s.db, eventID)
}

// CountPendingApplications reads the number of pending applications for a user.
func (s *Service) CountPendingApplications(ctx context.Context, userID string) (int, error) {
	return eventrepo.CountPendingApplications(ctx, s.db, userID)
}
