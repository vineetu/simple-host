package ops

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/ops/repo"
	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/analytics"
	sites "github.com/vsriram/simple-host/host/sites/repo"
)

// Reporting reads operator and owner traffic reports from the same aggregates.
type Reporting struct{ database *sql.DB }

func NewReporting(database *sql.DB) *Reporting { return &Reporting{database: database} }

type CountryStat struct {
	Country     string `json:"country"`
	CountryName string `json:"country_name"`
	analytics.Split
}
type SiteViews = analytics.SiteViews

// AnalyticsSite is the site the analytics reads report on: the caller's own
// by name, or (owner set to another handle) any account's for the platform
// admin. Anyone else naming another owner gets the same site_not_found as a
// site that does not exist, so the parameter cannot be used to learn which
// handles or sites exist.
func (s *Reporting) AnalyticsSite(ctx context.Context, user *model.User, owner, name string) (model.Site, error) {
	notFound := &appsites.Error{Code: "site_not_found", Message: "site not found"}
	ownerID := user.ID
	if owner = strings.TrimSpace(owner); owner != "" && !strings.EqualFold(owner, user.Handle.String) {
		if !user.IsAdmin {
			return model.Site{}, notFound
		}
		u, err := repo.GetAnalyticsOwner(ctx, s.database, owner)
		if errors.Is(err, sql.ErrNoRows) {
			return model.Site{}, notFound
		}
		if err != nil {
			return model.Site{}, err
		}
		ownerID = u.ID
	}
	site, err := sites.GetSiteByUser(ctx, s.database, ownerID, name)
	if errors.Is(err, sql.ErrNoRows) {
		return site, notFound
	}
	return site, err
}
func (s *Reporting) SiteAnalytics(ctx context.Context, id string, days int) (*analytics.SiteAnalytics, error) {
	return analytics.GetSiteAnalytics(ctx, s.database, id, days)
}
func (s *Reporting) SiteGeo(ctx context.Context, id string, days int) ([]CountryStat, error) {
	rows, err := analytics.GetSiteGeo(ctx, s.database, id, days)
	if err != nil {
		return nil, err
	}
	out := make([]CountryStat, 0, len(rows))
	for _, c := range rows {
		out = append(out, CountryStat{Country: c.Country, CountryName: analytics.CountryName(c.Country), Split: c.Split})
	}
	return out, nil
}
func (s *Reporting) SiteTop(ctx context.Context, id string, days int) ([]analytics.PageStat, []analytics.ReferrerStat, error) {
	return analytics.GetSiteTop(ctx, s.database, id, days)
}
func (s *Reporting) AnalyticsSummary(ctx context.Context, id string, days int, all bool) ([]analytics.SiteAnalyticsSummary, error) {
	return analytics.GetAnalyticsSummary(ctx, s.database, id, days, all)
}
func (s *Reporting) AdminSiteViews(ctx context.Context, since time.Time) (map[string]int64, error) {
	return analytics.AdminSiteViews(ctx, s.database, since)
}
func (s *Reporting) PersonViewsBySite(ctx context.Context, id string, all bool) (map[string]SiteViews, error) {
	return analytics.PersonViewsBySite(ctx, s.database, id, all)
}

// OwnerMarks records signed-in owner addresses for traffic classification.
type OwnerMarks struct{ marks *analytics.OwnerMarks }

func NewOwnerMarks(db *sql.DB, secret, salt string) *OwnerMarks {
	return &OwnerMarks{marks: analytics.NewOwnerMarks(db, secret, salt)}
}
func (m *OwnerMarks) Mark(ctx context.Context, user, ip string) error {
	return m.marks.Mark(ctx, user, ip)
}

func (s *Reporting) AccountCount(ctx context.Context) int {
	n, err := repo.AccountCount(ctx, s.database)
	if err != nil {
		return -1
	}
	return n
}
