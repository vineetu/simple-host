package events

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/vsriram/simple-host/internal/workers"
)

// Mailer is the shared transactional sender supplied by the composition root.
// Optional notice/reply/context capabilities are detected before delivery.
type Mailer interface {
	SendSignInCode(toEmail, code, link string) error
}

// TrackContentSender records delivery outcomes without exposing provider errors.
func TrackContentSender(m Mailer, sender NoticeSender, tracker *workers.Tracker) Mailer {
	return trackedContentSender{Mailer: m, notice: sender, tracker: tracker}
}

// ArchiveWarningSender preserves the optional reply-to and cancellation capabilities.
func ArchiveWarningSender(m Mailer) func(context.Context, string, string, string, string) error {
	sender, ok := m.(interface {
		SendNoticeReplyTo(string, string, string, string) error
	})
	if !ok {
		return nil
	}
	return func(ctx context.Context, to, replyTo, subject, body string) error {
		if s, ok := sender.(interface {
			SendNoticeReplyToCtx(context.Context, string, string, string, string) error
		}); ok {
			return s.SendNoticeReplyToCtx(ctx, to, replyTo, subject, body)
		}
		return sender.SendNoticeReplyTo(to, replyTo, subject, body)
	}
}

type trackedContentSender struct {
	Mailer
	notice interface {
		SendNotice(string, string, string) error
	}
	tracker *workers.Tracker
}

func (s trackedContentSender) CanSendNotice() bool {
	if ready, ok := s.Mailer.(interface{ CanSendNotice() bool }); ok {
		return ready.CanSendNotice()
	}
	return true
}
func (s trackedContentSender) SendNotice(to, subject, body string) error {
	return s.SendNoticeCtx(context.Background(), to, subject, body)
}
func (s trackedContentSender) SendNoticeCtx(ctx context.Context, to, subject, body string) error {
	if err := sendMailNotice(ctx, s.notice, to, subject, body); err != nil {
		// Provider errors can contain the recipient or credentials. Keep those
		// out of both the status and the existing service log.
		err = contentMailError(err)
		s.tracker.Fail("send mail", err)
		return err
	}
	s.tracker.Progress()
	return nil
}

// Mail errors may echo recipients or provider credentials; expose only known causes.
func contentMailError(err error) error {
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
