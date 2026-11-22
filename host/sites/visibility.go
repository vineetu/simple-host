package sites

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"

	"github.com/vsriram/simple-host/host/sites/repo"
)

// VisibilityInput holds the values for SetVisibility.
type VisibilityInput struct{ UserID, Name, Visibility string }

// SetVisibility updates visibility and the corresponding on-disk marker under the site lock.
func (s *Service) SetVisibility(ctx context.Context, in VisibilityInput) error {
	userID, name, visibility := in.UserID, in.Name, in.Visibility
	unlock := s.lockSite(userID, name)
	defer unlock()
	site, err := repo.GetSiteByUser(ctx, s.database, userID, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &Error{Code: "site_not_found"}
		}
		return err
	}
	if site.Suspended() {
		return &Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	if err := repo.SetSiteVisibility(ctx, s.database, site.ID, visibility); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &Error{Code: "not_found", Message: "site not found"}
		}
		return err
	}
	return nil
}

// SetAllowedOrigins sets the extra origins that may call a site's API
// cross-origin, so a page hosted anywhere can use the site as its backend.
// Each origin is scheme://host with no path, at most 20; the cleaned list is
// returned. Refusals: site_not_found, site_suspended, invalid_origin and
// too_many_origins.
func (s *Service) SetAllowedOrigins(ctx context.Context, userID, name string, origins []string) ([]string, error) {
	site, err := repo.GetSiteByUser(ctx, s.database, userID, name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &Error{Code: "site_not_found", Message: "site not found"}
	}
	if err != nil {
		return nil, err
	}
	if site.Suspended() {
		return nil, &Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	clean := make([]string, 0, len(origins))
	for _, o := range origins {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		u, err := url.Parse(o)
		if err != nil || u.Scheme == "" || u.Host == "" || (u.Path != "" && u.Path != "/") {
			return nil, &Error{Code: "invalid_origin", Message: "each origin must be scheme://host with no path, e.g. https://you.github.io"}
		}
		clean = append(clean, u.Scheme+"://"+u.Host)
	}
	if len(clean) > 20 {
		return nil, &Error{Code: "too_many_origins", Message: "too many origins (max 20)"}
	}
	if err := repo.SetAllowedOrigins(ctx, s.database, site.ID, strings.Join(clean, ",")); err != nil {
		return nil, err
	}
	return clean, nil
}
