package sites

import (
	"context"
	"fmt"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
)

// CheckFileCaps checks the projected disk use before writing a version.
func (s *Service) CheckFileCaps(ctx context.Context, enabled bool, cutoffText string, limits func() (int, int), userID string, site model.Site, files map[string][]byte, next int, publish bool, retention func() (int, error)) error {
	if !enabled {
		return nil
	}
	siteMB, accountMB := limits()
	cutoff, err := time.Parse(time.RFC3339, cutoffText)
	if err != nil || site.CreatedAt.Before(cutoff) {
		siteMB = 0
	}
	if siteMB == 0 && accountMB == 0 {
		return nil
	}
	f, err := s.disk.SiteFootprint(userID, site.Name)
	if err != nil {
		return &Error{Code: "site_measure_failed", Message: "could not measure website files"}
	}
	var incoming int64
	for _, b := range files {
		incoming += int64(len(b))
	}
	keep, err := retention()
	if err != nil {
		return err
	}
	versions, err := repo.ListVersionsBySite(ctx, s.database, site.ID)
	if err != nil {
		return err
	}
	versions = append([]model.Version{{VersionNumber: next}}, versions...)
	floor := 0
	if keep > 0 && len(versions) > keep {
		floor = versions[keep-1].VersionNumber
	}
	projected := f.Total() - f.Versions[next] + incoming
	active := site.ActiveVersion
	if publish {
		projected += incoming - f.Live
		active = next
	}
	for _, v := range versions {
		if floor > 0 && v.VersionNumber < floor && v.VersionNumber != active && v.VersionNumber != next {
			projected -= f.Versions[v.VersionNumber]
		}
	}
	if siteMB > 0 && projected > int64(siteMB)<<20 {
		return &Error{Code: "site_total_too_large", Message: fmt.Sprintf("This website may use at most %d MB including its live files and kept versions; it currently uses %.1f MB. This deploy would use %.1f MB.", siteMB, float64(f.Total())/(1<<20), float64(projected)/(1<<20))}
	}
	if accountMB > 0 {
		current, err := s.disk.AccountFileBytes(userID)
		if err != nil {
			return &Error{Code: "account_measure_failed", Message: "could not measure account files"}
		}
		after := current - f.Total() + projected
		if after > int64(accountMB)<<20 {
			return &Error{Code: "account_storage_full", Message: fmt.Sprintf("This account may use at most %d MB for website files and kept versions; it currently uses %.1f MB. This deploy would use %.1f MB.", accountMB, float64(current)/(1<<20), float64(after)/(1<<20))}
		}
	}
	return nil
}
