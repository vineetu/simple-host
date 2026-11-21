package members

import (
	"context"

	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
)

func (s *Service) RegistrationSettingsLookup(ctx context.Context, eventID string) ([]model.SignupQuestion, bool, error) {
	return eventrepo.RegistrationSettings(ctx, s.db, eventID)
}

func (s *Service) CountPendingApplications(ctx context.Context, userID string) (int, error) {
	return hackmembers.Count(ctx, s.db, hackmembers.Filter{UserID: userID, Via: "application", Status: "pending"})
}
