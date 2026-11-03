package teams

// TODO(4.2): move to repository/hackteams.

import (
	"context"
	"github.com/vsriram/simple-host/internal/db"
)

func (s *Service) teamDeadlinePassed(ctx context.Context, teamID string) (bool, error) {
	var passed bool
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(`+db.EffectiveDeadlineSQL+` <= clock_timestamp(), false)
		  FROM event_teams t JOIN events e ON e.id = t.event_id WHERE t.id = $1`, teamID).Scan(&passed)
	return passed, err
}
