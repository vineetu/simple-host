package teams

import (
	"context"

	"github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

func (s *Service) TeamRemovalNotice(ctx context.Context, eventID, userID string, out *string) error {
	return hackteams.TeamRemovalNotice(ctx, s.db, eventID, userID, out)
}

func (s *Service) LookingFor(ctx context.Context, teamID string, out *string) error {
	return hackteams.LookingFor(ctx, s.db, teamID, out)
}

func (s *Service) AdminSuccessor(ctx context.Context, teamID, userID string, out *string) error {
	return hackteams.AdminSuccessor(ctx, s.db, teamID, userID, out)
}

func (s *Service) PendingRequests(ctx context.Context, teamID string, out *int) error {
	return hackteams.PendingRequests(ctx, s.db, teamID, out)
}
