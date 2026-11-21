package members

import (
	"context"

	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

func (s *Service) MemberApprovalStatusLookup(ctx context.Context, eventID string, userID string) (string, error) {
	return eventrepo.MemberApprovalStatus(ctx, s.db, eventID, userID)
}

func (s *Service) RegistrationSettingsLookup(ctx context.Context, eventID string) ([]model.SignupQuestion, bool, error) {
	return eventrepo.RegistrationSettings(ctx, s.db, eventID)
}

func (s *Service) CountPendingApplications(ctx context.Context, userID string) (int, error) {
	return eventrepo.CountPendingApplications(ctx, s.db, userID)
}
