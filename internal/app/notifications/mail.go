package notifications

import (
	"context"
	"errors"
	"fmt"
	"github.com/vsriram/simple-host/internal/email"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/notifications"
	"github.com/vsriram/simple-host/internal/workers"
	"net"
	"strings"
	"time"
)

func (s *Service) NotifySuspension(ctx context.Context, n model.Notification, mailer email.NoticeSender, tracker *workers.Tracker) {
	to, err := repo.SiteOwnerEmail(ctx, s.database, n.SiteID)
	if err != nil || !strings.Contains(to, "@") {
		return
	}
	claimed, err := s.ClaimNotificationEmail(ctx, n.ID)
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
		if err := repo.ResetSuspensionEmail(retryCtx, s.database, n.ID); err != nil {
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

func (s *Service) SweepSuspensionEmails(ctx context.Context, days int, mailer email.NoticeSender, t *workers.Tracker) {
	t.Attempt()
	n, err := s.SweepNotificationsCount(ctx, days)
	if err != nil {
		t.Fail("sweep history", err)
	}
	if n > 0 {
		t.Progress()
	}
	ids, readErr, err := repo.SuspensionEmails(ctx, s.database)
	if err != nil {
		t.Fail("list suspension emails", err)
		return
	}
	if readErr != nil {
		t.Fail("read suspension emails", readErr)
	}
	for _, id := range ids {
		if n, err := s.GetNotification(ctx, id); err == nil && mailer != nil {
			s.NotifySuspension(ctx, n, mailer, t)
		}
	}
}
