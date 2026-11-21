package members

import (
	"context"
	"errors"

	model "github.com/vsriram/simple-host/products/hack/model"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// ErrArchiveLinkInvalid refuses an archive link whose holder no longer may
// download it.
var ErrArchiveLinkInvalid = errors.New("archive link holder no longer qualifies")

// ArchiveHolder re-checks, at download time, the person an archive link was
// made for: still an organiser of the event it names (teamID "") or still an
// approved participant on the same team. It answers the event and, for a
// team archive, the team's slug.
func (s *Service) ArchiveHolder(ctx context.Context, slug, eventID, userID, teamID string) (model.Event, string, error) {
	role := "organiser"
	if teamID != "" {
		role = "participant"
	}
	c, err := s.Caller(ctx, slug, userID, false, false, role)
	if err != nil || c.Event.ID != eventID {
		return c.Event, "", ErrArchiveLinkInvalid
	}
	if teamID == "" {
		return c.Event, "", nil
	}
	team, err := teamrepo.GetEventTeamByID(ctx, s.db, teamID)
	if err != nil || c.Member.TeamID.String != teamID || team.EventID != eventID {
		return c.Event, "", ErrArchiveLinkInvalid
	}
	return c.Event, team.Slug, nil
}
