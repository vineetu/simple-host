package sites

import (
	"context"
	"fmt"
	"time"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// checkFileCaps checks the projected disk use before writing a version.
func (s *Service) checkFileCaps(ctx context.Context, user *model.User, site model.Site, files map[string][]byte, next int, publish bool) error {
	if !s.deps.FileCaps {
		return nil
	}
	siteMB, accountMB := s.deps.CapLimits(ctx, user)
	cutoff, err := time.Parse(time.RFC3339, s.deps.CapCutoff)
	if err != nil || site.CreatedAt.Before(cutoff) {
		siteMB = 0
	}
	if siteMB == 0 && accountMB == 0 {
		return nil
	}
	f, err := s.disk.SiteFootprint(user.ID, site.Name)
	if err != nil {
		return &Error{Code: "site_measure_failed", Message: "could not measure website files"}
	}
	var incoming int64
	for _, b := range files {
		incoming += int64(len(b))
	}
	keep, err := s.deps.Retention(ctx, user, site)
	if err != nil {
		return err
	}
	versions, err := repo.ListVersionsBySite(ctx, s.database, site.ID)
	if err != nil {
		return err
	}
	versions = append([]model.Version{{VersionNumber: next}}, versions...)
	floor := RetainedFloor(versions, keep)
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
		current, err := s.disk.AccountFileBytes(user.ID)
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

// RetainedFloor is the oldest version a keep of keep retains, 0 for all.
// versions are newest first and are the actual rows, so gaps left by earlier
// pruning do not reduce the count. The live version is kept in addition.
func RetainedFloor(versions []model.Version, keep int) int {
	if keep <= 0 || len(versions) <= keep {
		return 0
	}
	return versions[keep-1].VersionNumber
}
