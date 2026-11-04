package sites

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
	"log"
	"strings"
	"time"
)

// IdleSite is a candidate for idle cleanup.
type IdleSite = repo.IdleSite

// IdleExempt describes accounts and sites excluded from cleanup.
type IdleExempt = repo.IdleExempt

// IdleNoticeSender sends the notice before its transaction commits.
type IdleNoticeSender interface {
	SendNoticeReplyTo(string, string, string, string) error
}

// IdleArchiveDeps supplies instance policy and mail collaborators.
type IdleArchiveDeps struct {
	Exempt                repo.IdleExempt
	After, Grace          time.Duration
	Mailer                IdleNoticeSender
	NewToken              func() (string, []byte)
	SiteAddress, OwnerApp func(IdleSite) string
	Link                  func(string, string) string
	ReplyTo               func() string
	Span                  func(time.Duration) string
	Count                 func(int, string) string
}

// IdleArchiveInput identifies a listed candidate and the cleanup clock.
type IdleArchiveInput struct {
	Site IdleSite
	Now  time.Time
}

// WarnIdleSite marks the warning and emails it in one transaction: the mark
// re-checks that the site still qualifies (under its row lock), the email is
// sent, and only then is the mark committed. A failed send rolls it back; a
// crash after the send and before the commit leaves no warning on record, so
// the next run sends it again (a repeat email, never a removal nobody was
// told about).
func (svc *Service) WarnIdleSite(ctx context.Context, in IdleArchiveInput) {
	s, now, mailer := in.Site, in.Now, svc.deps.IdleArchive.Mailer
	d := svc.deps.IdleArchive
	if !strings.Contains(s.OwnerEmail, "@") {
		return
	}
	tok, hash := d.NewToken()
	if ctx.Err() != nil {
		return
	}
	txCtx, cancelTx := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelTx()
	stopCancel := context.AfterFunc(ctx, cancelTx)
	defer stopCancel()
	tx, err := svc.database.BeginTx(txCtx, nil)
	if err != nil {
		ReportWorkerFailure(ctx, "begin idle warning transaction", err)
		log.Printf("idle-site cleanup: warn %s: %v", s.SiteID, err)
		return
	}
	defer tx.Rollback()
	removeAt := now.Add(d.Grace)
	if err := repo.MarkIdleWarned(ctx, tx, d.Exempt, s.SiteID, hash, now.Add(-d.After), removeAt); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			ReportWorkerFailure(ctx, "mark idle warning", err)
			log.Printf("idle-site cleanup: mark %s warned: %v", s.SiteID, err)
		}
		return
	}
	days := int(now.Sub(s.LastActivity).Hours() / 24)
	removeOn := removeAt.UTC().Format("2 January 2006")
	subject := "Your site " + s.Name + " has had no visitors for " + fmt.Sprint(days) + " days"
	text := fmt.Sprintf(`Your Simple Host site %s (%s) has had no visitors, no new versions and no saved data for %d days.

Keep it online (opens a page with a Keep button, no sign-in):
%s

To download a copy of its files and saved data, sign in on your Simple Host page:
%s

If you do nothing, on %s it moves to Recently deleted, where it can still be restored for %s before it is removed for good. A site stays up for good if you mark it Keep on your Simple Host page, or give it its own domain or name.

Questions? Just reply to this email.

Simple Host
`, s.Name, d.SiteAddress(s), days, d.Link("keep", tok), d.OwnerApp(s), removeOn, d.Span(svc.deps.DeletedRetention))
	// Keep the token/mark transaction alive through a successful send.
	if !stopCancel() || ctx.Err() != nil {
		return
	}
	sendErr := sendIdleNotice(ctx, mailer, s.OwnerEmail, d.ReplyTo(), subject, text)
	finishCtx, cancelFinish := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelFinish()
	stopFinish := context.AfterFunc(finishCtx, cancelTx)
	defer stopFinish()
	if err := sendErr; err != nil {
		ReportWorkerFailure(ctx, "send idle warning", err)
		log.Printf("idle-site cleanup: warn %s: %v", s.SiteID, err)
		return
	}
	if err := tx.Commit(); err != nil {
		ReportWorkerFailure(ctx, "commit idle warning", err)
		log.Printf("idle-site cleanup: warn %s: email sent but not recorded (it goes again next run): %v", s.SiteID, err)
		return
	}
	ReportWorkerProgress(ctx)
	log.Printf("idle-site cleanup: warned owner of %s (%s)", s.Name, s.SiteID)
}

// RemoveIdleSite moves a site to Recently deleted and emails the restore
// link, in the transaction that moves it: the removal re-checks that the site
// still qualifies (a Keep, deploy, save or new domain since the list was read
// rolls it back), the email goes out before the files move and the commit,
// and a failed send leaves the site where it is for the next run.
func (svc *Service) RemoveIdleSite(ctx context.Context, in IdleArchiveInput) {
	s, now, mailer := in.Site, in.Now, svc.deps.IdleArchive.Mailer
	d := svc.deps.IdleArchive
	if !strings.Contains(s.OwnerEmail, "@") {
		return
	}
	unlock := svc.deps.LockSite(s.UserID, s.Name)
	defer unlock()
	site, err := repo.GetSiteByUser(ctx, svc.database, s.UserID, s.Name)
	if err != nil || site.ID != s.SiteID || site.Suspended() || site.Offline {
		return
	}
	tok, hash := d.NewToken()
	purge := time.Now().Add(svc.deps.DeletedRetention).UTC().Format("2 January 2006")
	// How long ago the warning went, as it was (the grace in force then may
	// differ from today's).
	warnedAgo := d.Span(d.Grace)
	if s.WarnedAt.Valid {
		if days := int(now.Sub(s.WarnedAt.Time) / (24 * time.Hour)); days >= 1 {
			warnedAgo = d.Count(days, "day")
		}
	}
	subject := "Your site " + s.Name + " was moved to Recently deleted"
	text := fmt.Sprintf(`Your Simple Host site %s had no visitors for over %s, and nobody chose to keep it after our email %s ago, so it was moved to Recently deleted.

Restore it exactly as it was (opens a page with a Restore button, no sign-in), until %s:
%s

You can also restore or download it after signing in on your Simple Host page:
%s

After that it is removed for good. If you meant to let it go, there is nothing to do.

Questions? Just reply to this email.

Simple Host
`, s.Name, d.Span(d.After+d.Grace), warnedAgo, purge, d.Link("restore", tok), d.OwnerApp(s))
	if ctx.Err() != nil {
		return
	}
	txCtx, cancelTx := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelTx()
	stopCancel := context.AfterFunc(ctx, cancelTx)
	defer stopCancel()
	var cancelFinish context.CancelFunc
	var stopFinish func() bool
	defer func() {
		if stopFinish != nil {
			stopFinish()
			cancelFinish()
		}
	}()
	trashSvc := *svc
	trashSvc.deps.DeletedTransaction = func(tx *sql.Tx) error {
		if err := repo.MarkIdleRemoved(ctx, tx, d.Exempt, site.ID, hash, now, d.Grace); err != nil {
			return err
		}
		// Trash owns the transaction; detach only when its warning mark is
		// ready and bound the remaining file move/commit after the send.
		if !stopCancel() || ctx.Err() != nil {
			return ctx.Err()
		}
		sendErr := sendIdleNotice(ctx, mailer, s.OwnerEmail, d.ReplyTo(), subject, text)
		var finishCtx context.Context
		finishCtx, cancelFinish = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		stopFinish = context.AfterFunc(finishCtx, cancelTx)
		return sendErr
	}
	err = trashSvc.Trash(txCtx, TrashInput{Site: site})
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			ReportWorkerFailure(ctx, "remove idle site", err)
			log.Printf("idle-site cleanup: remove %s: %v", s.SiteID, err)
		}
		return
	}
	ReportWorkerProgress(ctx)
	log.Printf("idle-site cleanup: moved %s (%s) to Recently deleted", s.Name, s.SiteID)
}

// Use the production sender's cancellation capability while retaining local senders.
func sendIdleNotice(ctx context.Context, sender IdleNoticeSender, to, replyTo, subject, body string) error {
	if s, ok := sender.(interface {
		SendNoticeReplyToCtx(context.Context, string, string, string, string) error
	}); ok {
		return s.SendNoticeReplyToCtx(ctx, to, replyTo, subject, body)
	}
	return sender.SendNoticeReplyTo(to, replyTo, subject, body)
}
