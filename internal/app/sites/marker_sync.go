package sites

import (
	"context"

	"github.com/vsriram/simple-host/internal/model"
)

// SyncUnlistedMarker evaluates the current address policy before changing the marker.
func (s *Service) SyncUnlistedMarker(ctx context.Context, site model.Site) error {
	want, err := s.deps.WantMarker(ctx, site)
	if err != nil {
		return err
	}
	return s.disk.SetUnlisted(site.UserID, site.Name, want)
}
