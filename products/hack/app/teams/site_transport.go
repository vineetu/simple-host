package teams

import (
	"context"
	"log"

	"github.com/vsriram/simple-host/internal/model"
	events "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	repo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

func (s *Service) SiteEventBySlug(ctx context.Context, slug string) (model.Event, error) {
	return events.GetEventBySlug(ctx, s.db, slug)
}
func (s *Service) SiteEventByAccount(ctx context.Context, accountID string) (model.Event, error) {
	return events.GetEventByAccount(ctx, s.db, accountID)
}
func (s *Service) SiteTeamIdentity(ctx context.Context, memberID, teamID string) (model.TeamIdentity, error) {
	return repo.ResolveTeamIdentity(ctx, s.db, memberID, teamID)
}
func (s *Service) SitePinnedVersion(ctx context.Context, accountID, name string) (int, bool, error) {
	return repo.PinnedVersionOfSite(ctx, s.db, accountID, name)
}
func (s *Service) SiteTeams(ctx context.Context, eventID string) ([]model.EventTeam, error) {
	return repo.ListEventTeams(ctx, s.db, eventID)
}

// SweepSites pins overdue teams before removing empty teams and orphan sites.
// Progress callbacks retain the background owner's existing accounting.
func (s *Service) SweepSites(ctx context.Context, trash func(context.Context, string, string) error, progress func(), failure func(string, error)) {
	if err := repo.PinDueTeams(ctx, s.db, "", progress); err != nil {
		failure("pin due teams", err)
	}
	if err := repo.DeleteEmptyOpenTeams(ctx, s.db, progress); err != nil {
		failure("delete empty open teams", err)
	}
	orphans, err := repo.OrphanTeamSites(ctx, s.db, "")
	if err != nil {
		failure("list orphan team sites", err)
		return
	}
	for _, site := range orphans {
		if err := trash(ctx, site.UserID, site.Name); err != nil {
			failure("trash orphan team site", err)
		} else {
			progress()
			log.Printf("hack sweep: site %s of a removed team moved to Recently deleted", site.Name)
		}
	}
}
