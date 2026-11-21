package notify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	repo "github.com/vsriram/simple-host/host/data/repo/notify"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/model"
)

// NotifySuspension mails n's site owner that n was suspended (no mailer:
// nothing).
func (e *Engine) NotifySuspension(ctx context.Context, mailer email.NoticeSender, n model.Notification) {
	if mailer == nil {
		return
	}
	tracker := e.retention
	to, err := repo.SiteOwnerEmail(ctx, e.db, n.SiteID)
	if err != nil || !strings.Contains(to, "@") {
		return
	}
	claimed, err := repo.ClaimNotificationEmail(ctx, e.db, n.ID)
	if err != nil {
		tracker.Fail("claim suspension email", err)
	}
	if err != nil || !claimed {
		return
	}
	if err = email.SendNoticeCtx(ctx, mailer, to, "Simple Host notification paused", "Notifications for "+n.Site+" have been suspended after repeated delivery failures. Check the receiver, then resume the subscription in Notifications."); err != nil {
		tracker.Fail("send suspension email", notificationMailError(err))
		retryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := repo.ResetSuspensionEmail(retryCtx, e.db, n.ID); err != nil {
			tracker.Fail("reset suspension email", err)
		}
	} else {
		tracker.Progress()
	}
}

// Mail errors may echo recipients or provider credentials; expose only known causes.
func notificationMailError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return errors.New("mail provider timeout")
	}
	if err.Error() == "RESEND_API_KEY not configured" {
		return errors.New("mail provider is not configured")
	}
	var status int
	if n, _ := fmt.Sscanf(err.Error(), "resend returned status %d", &status); n == 1 {
		return fmt.Errorf("mail provider returned HTTP %d", status)
	}
	return errors.New("mail provider delivery failed")
}

// Sweep prunes delivery history and mails suspension notices still owed.
func (e *Engine) Sweep(ctx context.Context, mailer email.NoticeSender) {
	t := e.retention
	t.Attempt()
	n, err := repo.SweepNotificationsCount(ctx, e.db, e.limits.NotifyHistoryDays)
	if err != nil {
		t.Fail("sweep history", err)
	}
	if n > 0 {
		t.Progress()
	}
	ids, readErr, err := repo.SuspensionEmails(ctx, e.db)
	if err != nil {
		t.Fail("list suspension emails", err)
		return
	}
	if readErr != nil {
		t.Fail("read suspension emails", readErr)
	}
	for _, id := range ids {
		if n, err := repo.GetNotification(ctx, e.db, id); err == nil && mailer != nil {
			e.NotifySuspension(ctx, mailer, n)
		}
	}
}
