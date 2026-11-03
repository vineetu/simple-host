package teams

import (
	"context"
	"github.com/vsriram/simple-host/internal/repository/hackteams"
)

// TeamRemovalNotice reads a participant's removal notice.
func (s *Service) TeamRemovalNotice(ctx context.Context, eventID, userID string, out *string) error {
	return hackteams.TeamRemovalNotice(ctx, s.db, eventID, userID, out)
}

// LookingFor reads a team's finding note.
func (s *Service) LookingFor(ctx context.Context, teamID string, out *string) error {
	return hackteams.LookingFor(ctx, s.db, teamID, out)
}

// AdminSuccessor reads the next team administrator.
func (s *Service) AdminSuccessor(ctx context.Context, teamID, userID string, out *string) error {
	return hackteams.AdminSuccessor(ctx, s.db, teamID, userID, out)
}

// PendingRequests reads the team's request count.
func (s *Service) PendingRequests(ctx context.Context, teamID string, out *int) error {
	return hackteams.PendingRequests(ctx, s.db, teamID, out)
}
