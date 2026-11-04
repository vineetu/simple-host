package operations

import (
	"context"
	"fmt"
	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/email"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
	"log"
	"strings"
	"time"
)

// emailDomainFailing tells the owner their domain has stopped working and
// what happens next. Marked as sent only when it was.
func (h *Reporting) NotifyDomainFailing(ctx context.Context, d model.BoundDomain, siteName, reason string, mailer email.NoticeSender, ed *edition.Edition) {
	to, err := repo.GetSiteOwnerEmail(ctx, h.database, d.SiteID)
	if err != nil || !strings.Contains(to, "@") {
		return
	}
	if reason == "" {
		reason = "it does not answer over HTTPS"
	}
	subject := "Your domain " + d.Domain + " has stopped working"
	releaseIn := ed.Limits().DomainLapseAfter - ed.Limits().DomainLapseWarnAfter
	releaseAt := time.Now().Add(releaseIn)
	text := fmt.Sprintf(`Your site %s is connected to https://%s/, and that address has failed every check for %s.

What the last check saw: %s

If you moved the domain or let it lapse on purpose, there is nothing to do. Otherwise, check the domain's DNS records at your registrar (the address record, and the TXT record _simple-host.<domain>).

If it is still failing %s from now, %s is disconnected from the site: the site serves at its own Simple Host address again. Connecting the domain again (by you or whoever holds it then) needs its DNS ownership record (TXT _simple-host.<domain>) once more.

Simple Host
`, siteName, d.Domain, ed.Span(ed.Limits().DomainLapseWarnAfter), reason, ed.Span(releaseIn), d.Domain)
	if err := email.SendNoticeCtx(ctx, mailer, to, subject, text); err != nil {
		sites.ReportWorkerFailure(ctx, "send domain failure notice", err)
		return
	}
	if err := repo.MarkDomainLapseNotified(ctx, h.database, d.SiteID, d.Domain, releaseAt); err != nil {
		sites.ReportWorkerFailure(ctx, "mark domain failure notice", err)
	}
	log.Printf("domain %s: owner emailed about failing checks", d.Domain)
}
