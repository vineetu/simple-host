package ops

import (
	"context"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"log"
	"time"
)

// IdleRemoveAfter is how long a site goes unused before the cleanup moves it
// to Recently deleted: IDLE_AFTER_DAYS plus IDLE_GRACE_DAYS.
func IdleRemoveAfter(ed *edition.Edition) time.Duration {
	return ed.Limits().IdleAfter + ed.Limits().IdleGrace
}

// idleBatch caps the sites one run moves, so a first run on a long-neglected
// server does not remove everything at once.
const idleBatch = 50

// IdleEvidenceStatus says whether visit data can be trusted to call sites
// idle, and why not.
func (h *Reporting) IdleEvidenceStatus(ctx context.Context, now time.Time, ed *edition.Edition) (bool, string, time.Time, time.Time, error) {
	since, last, err := h.IdleEvidence(ctx)
	if err != nil {
		return false, "", since, last, err
	}
	switch {
	case since.IsZero() || since.After(now.Add(-IdleRemoveAfter(ed))):
		return false, "fewer than " + ed.Span(IdleRemoveAfter(ed)) + " of visit records on this server, so nothing can be called idle yet", since, last, nil
	case last.IsZero() || now.Sub(last) > 6*time.Hour:
		return false, "visit records are not being read (the analytics ingester has not run for six hours), so nothing is called idle", since, last, nil
	}
	return true, "", since, last, nil
}

// RunIdleCleanup is one pass: move the sites unused for IdleRemoveAfter to
// Recently deleted.
func (h *Reporting) RunIdleCleanup(ctx context.Context, now time.Time, ed *edition.Edition, on bool, ex sites.IdleExempt, archive *sites.Service) {
	if !on {
		return
	}
	trusted, why, _, _, err := h.IdleEvidenceStatus(ctx, now, ed)
	if err != nil {
		sites.ReportWorkerFailure(ctx, "idle cleanup", err)
		return
	}
	if !trusted {
		log.Printf("idle-site cleanup: skipped: %s", why)
		return
	}
	remove, err := h.ListIdleSitesToRemove(ctx, ex, now.Add(-IdleRemoveAfter(ed)), idleBatch)
	if err != nil {
		sites.ReportWorkerFailure(ctx, "idle cleanup", err)
		return
	}
	for _, s := range remove {
		archive.RemoveIdleSite(ctx, sites.IdleArchiveInput{Site: s, Now: now})
	}
}

// IdleDryRun is what a cleanup run would do now: whether visit data can be
// trusted (and why not), how far back it goes, and up to show sites a run
// would move to Recently deleted.
type IdleDryRun struct {
	Trusted         bool
	Why             string
	Since, LastRead time.Time
	WouldRemove     []model.IdleSite
}

// IdleCleanupDryRun answers the operator's idle-sites view.
func (h *Reporting) IdleCleanupDryRun(ctx context.Context, now time.Time, ed *edition.Edition, ex model.IdleExempt, show int) (IdleDryRun, error) {
	var out IdleDryRun
	var err error
	out.Trusted, out.Why, out.Since, out.LastRead, err = h.IdleEvidenceStatus(ctx, now, ed)
	if err != nil {
		return out, err
	}
	out.WouldRemove, err = h.ListIdleSitesToRemove(ctx, ex, now.Add(-IdleRemoveAfter(ed)), show)
	return out, err
}
