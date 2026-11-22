package sites

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// ViewerSignInSite is userID's site name when its named viewers can sign in
// on it now: named viewers on, not offline, not taken down. ok=false
// otherwise, a missing site included.
func (s *Service) ViewerSignInSite(ctx context.Context, userID, name string) (model.SitePasscodeRow, bool, error) {
	row, err := repo.GetSitePasscodeByName(ctx, s.database, userID, name)
	if errors.Is(err, sql.ErrNoRows) {
		return row, false, nil
	}
	if err != nil {
		return row, false, err
	}
	if !row.NamedViewers || row.Offline || s.disk.IsSuspended(row.UserID, row.Name) {
		return row, false, nil
	}
	return row, true, nil
}
