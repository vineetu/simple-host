package sites

import (
	"context"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
	"log"
)

// OwnerMarkDeps supplies best-effort visit classification.
type OwnerMarkDeps struct {
	Mark func(context.Context, string, string) error
}

// OwnerMarkInput identifies the signed-in visitor and site.
type OwnerMarkInput struct{ SiteID, UserID, IP string }

// NoteOwnerVisitorSignIn marks visits only for the site's owning account.
func (s *Service) NoteOwnerVisitorSignIn(ctx context.Context, in OwnerMarkInput) {
	owner, err := repo.SiteOwner(ctx, s.database, in.SiteID)
	if err != nil {
		log.Printf("owner marks: site owner site=%s: %v", in.SiteID, err)
		return
	}
	if owner != in.UserID {
		return
	}
	if err := s.deps.OwnerMarks.Mark(ctx, in.UserID, in.IP); err != nil {
		log.Printf("owner marks: visitor sign-in user=%s: %v", in.UserID, err)
	}
}
