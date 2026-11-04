package legacy

import (
	"context"
	"log"
	"time"

	repo "github.com/vsriram/simple-host/internal/repository/legacy"
	storagerules "github.com/vsriram/simple-host/internal/rules/storage"
)

// SweepSavedDataInput contains retention and history limits for a sweep.
type SweepSavedDataInput struct {
	UndoDays, IdempotencyHours, IdempotencyMaxPerSite, WatchKeepDays int
	HistoryCapBytes                                                  int64
}

// SweepSavedData performs one saved-data cleanup tick.
func (a *Adapter) SweepSavedData(ctx context.Context, in SweepSavedDataInput) {
	a.deps.SweepEvict()
	res, err := repo.PurgeOldDeletedItems(ctx, a.database, in.UndoDays)
	if err != nil {
		log.Printf("saved-data sweep: %v", err)
		return
	}
	items, _ := res.RowsAffected()
	res, err = repo.PurgeOldHistory(ctx, a.database, in.UndoDays)
	if err != nil {
		log.Printf("saved-data sweep: %v", err)
		return
	}
	hist, _ := res.RowsAffected()
	idem, err := repo.PurgeIdempotencyKeys(ctx, a.database, time.Duration(in.IdempotencyHours)*time.Hour, in.IdempotencyMaxPerSite)
	if err != nil {
		log.Printf("saved-data sweep (idempotency): %v", err)
	}
	watch, err := repo.PurgeDataWatch(ctx, a.database, in.WatchKeepDays)
	if err != nil {
		log.Printf("saved-data sweep (watch): %v", err)
	}
	sites, err := repo.SitesOverHistoryCap(ctx, a.database, in.HistoryCapBytes)
	if err != nil {
		log.Printf("saved-data sweep (cap): %v", err)
	}
	var thinned int64
	over := make(map[string]bool, len(sites))
	for _, id := range sites {
		over[id] = true
		thinned += a.ThinHistoryIfUseful(ctx, HistoryThinInput{SiteID: id, CapBytes: in.HistoryCapBytes})
	}
	a.deps.HistoryThinStuck.Range(func(k, _ any) bool {
		if !over[k.(string)] {
			a.deps.HistoryThinStuck.Delete(k)
		}
		return true
	})
	if hist+items+idem+thinned+watch > 0 {
		log.Printf("saved-data sweep: history=%d deleted_items=%d idempotency=%d thinned=%d watch=%d", hist, items, idem, thinned, watch)
	}
}

// SavedDataLimitsInput holds the configured saved-data rate limits.
type SavedDataLimitsInput struct {
	ReadBurst, ReadPerSec, AppendBurst, AppendPerMin, BoardWritesPerMin int
}

// RateLimit describes a token bucket's capacity and refill rate.
type RateLimit struct {
	Capacity, PerSecond float64
}

// SavedDataLimits holds the rate limits used by saved-data operations.
type SavedDataLimits struct {
	Read, Append, Board, Thin RateLimit
}

// ConfigureSavedData derives the token bucket settings for saved data.
func (a *Adapter) ConfigureSavedData(_ context.Context, in SavedDataLimitsInput) SavedDataLimits {
	return SavedDataLimits{
		Read:   RateLimit{float64(in.ReadBurst), float64(in.ReadPerSec)},
		Append: RateLimit{float64(in.AppendBurst), float64(in.AppendPerMin) / 60},
		Board:  RateLimit{float64(in.BoardWritesPerMin), float64(in.BoardWritesPerMin) / 60},
		Thin:   RateLimit{1, 1},
	}
}

// HistoryThinInput identifies a site's history and its byte cap.
type HistoryThinInput struct {
	SiteID   string
	CapBytes int64
}

func (a *Adapter) historyThinPointless(siteID string, size, capBytes int64) bool {
	v, ok := a.deps.HistoryThinStuck.Load(siteID)
	if !ok {
		return false
	}
	return storagerules.LegacyHistoryThinPointless(size, v.(int64), capBytes, true)
}

func (a *Adapter) thinHistory(ctx context.Context, in HistoryThinInput) int64 {
	n, err := repo.ThinSiteHistory(ctx, a.database, in.SiteID, in.CapBytes)
	if err != nil {
		log.Printf("saved-data thin site_id=%s: %v", in.SiteID, err)
		return 0
	}
	if size, err := repo.HistoryBytes(ctx, a.database, in.SiteID); err == nil && size > in.CapBytes {
		a.deps.HistoryThinStuck.Store(in.SiteID, size)
	} else {
		a.deps.HistoryThinStuck.Delete(in.SiteID)
	}
	return n
}

// ThinHistoryIfUseful thins a site's history unless a recent thin was ineffective.
func (a *Adapter) ThinHistoryIfUseful(ctx context.Context, in HistoryThinInput) int64 {
	size, err := repo.HistoryBytes(ctx, a.database, in.SiteID)
	if err != nil || a.historyThinPointless(in.SiteID, size, in.CapBytes) {
		return 0
	}
	return a.thinHistory(ctx, in)
}

// BoundHistory thins an over-cap site's history after a write when allowed.
func (a *Adapter) BoundHistory(ctx context.Context, in HistoryThinInput) {
	ctx = context.WithoutCancel(ctx)
	size, err := repo.HistoryBytes(ctx, a.database, in.SiteID)
	if err != nil || size <= in.CapBytes || a.historyThinPointless(in.SiteID, size, in.CapBytes) || !a.deps.HistoryThinAllowed(in.SiteID) {
		return
	}
	a.thinHistory(ctx, in)
}

// SiteRoomInput contains the growth and limit for a saved-data write.
type SiteRoomInput struct {
	SiteID           string
	Growth, MaxBytes int64
}

// SiteHasRoom reports whether a site's saved data can accommodate growth.
func (a *Adapter) SiteHasRoom(ctx context.Context, in SiteRoomInput) (bool, error) {
	if in.Growth <= 0 || in.MaxBytes <= 0 {
		return true, nil
	}
	used, err := repo.SiteDataUsage(ctx, a.database, in.SiteID)
	if err != nil {
		return false, err
	}
	return used+in.Growth <= in.MaxBytes, nil
}

// ReadBucketInput identifies the requester and resolved site for read limiting.
type ReadBucketInput struct {
	SiteID, APIKey, ClientIP string
}

// ReadBucket chooses the saved-data read-limit bucket for a requester.
func (a *Adapter) ReadBucket(ctx context.Context, in ReadBucketInput) string {
	if in.APIKey != "" {
		if u, ok, err := a.deps.VisitorResolveKey(ctx, in.APIKey); err == nil && ok {
			return "user:" + u.ID
		}
	}
	return "site:" + in.SiteID + "|" + in.ClientIP
}

// HistoryThinCheckInput identifies a history size and its configured cap.
type HistoryThinCheckInput struct {
	SiteID         string
	Size, CapBytes int64
}

// HistoryThinPointless reports whether the last ineffective thin is still current.
func (a *Adapter) HistoryThinPointless(in HistoryThinCheckInput) bool {
	return a.historyThinPointless(in.SiteID, in.Size, in.CapBytes)
}
