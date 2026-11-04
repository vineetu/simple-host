package operations

import (
	"context"
	"errors"
	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/edition"
	"log"
	"time"
)

// idleEvidenceOK says whether visit data can be trusted to call sites idle,
// and why not.
func (h *Reporting) IdleEvidenceStatus(ctx context.Context, now time.Time, ed *edition.Edition) (bool, string, time.Time, time.Time, error) {
	since, last, err := h.IdleEvidence(ctx)
	if err != nil {
		return false, "", since, last, err
	}
	switch {
	case since.IsZero() || since.After(now.Add(-ed.Limits().IdleAfter)):
		return false, "fewer than " + ed.Span(ed.Limits().IdleAfter) + " of visit records on this server, so nothing can be called idle yet", since, last, nil
	case last.IsZero() || now.Sub(last) > 6*time.Hour:
		return false, "visit records are not being read (the analytics ingester has not run for six hours), so nothing is called idle", since, last, nil
	}
	return true, "", since, last, nil
}

// runIdleCleanup is one pass: drop warnings of sites active again, remove
// sites warned idleGrace ago, warn newly idle sites, within the email cap.
func (h *Reporting) RunIdleCleanup(ctx context.Context, now time.Time, ed *edition.Edition, on, hasMailer bool, ex sites.IdleExempt, budget int, archive *sites.Service) {
	if !on {
		return
	}
	ok := hasMailer
	if !ok {
		sites.ReportWorkerFailure(ctx, "send idle notices", errors.New("no mailer configured"))
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
	if n, err := h.ClearStaleIdleWarnings(ctx, ex); err != nil {
		sites.ReportWorkerFailure(ctx, "idle cleanup", err)
		return
	} else if n > 0 {
		sites.ReportWorkerProgress(ctx)
		log.Printf("idle-site cleanup: %d warned site(s) active again", n)
	}

	remove, err := h.ListIdleSitesToRemove(ctx, ex, now, ed.Limits().IdleGrace, budget)
	if err != nil {
		sites.ReportWorkerFailure(ctx, "idle cleanup", err)
		return
	}
	for _, s := range remove {
		if budget <= 0 {
			break
		}
		budget--
		archive.RemoveIdleSite(ctx, sites.IdleArchiveInput{Site: s, Now: now})
	}

	if budget <= 0 {
		log.Printf("idle-site cleanup: email cap reached; the rest wait for the next run")
		return
	}
	warn, err := h.ListIdleSitesToWarn(ctx, ex, now.Add(-ed.Limits().IdleAfter), budget)
	if err != nil {
		sites.ReportWorkerFailure(ctx, "idle cleanup", err)
		return
	}
	for _, s := range warn {
		if budget <= 0 {
			break
		}
		budget--
		archive.WarnIdleSite(ctx, sites.IdleArchiveInput{Site: s, Now: now})
	}
}
