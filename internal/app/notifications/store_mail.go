package notifications

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/email"
	"github.com/vsriram/simple-host/internal/model"
	legacy "github.com/vsriram/simple-host/internal/repository/legacy"
	repo "github.com/vsriram/simple-host/internal/repository/notifications"
)

type StoreMail struct {
	Database                *sql.DB
	Mailer                  any
	EachMinutes, DailyHours int
	Tracker                 interface {
		Attempt()
		Fail(string, error)
		SetPending(int64)
		Progress()
	}
	ExportBase, MainSiteURL string
	StopToken               func(string, string) string
	MailError               func(error) error
}

func (s *StoreMail) SendSubmissions(ctx context.Context) int {
	t := s.Tracker
	t.Attempt()
	mailer, ok := s.Mailer.(email.NoticeSender)
	each := time.Duration(s.EachMinutes) * time.Minute
	daily := time.Duration(s.DailyHours) * time.Hour
	due, err := legacy.DueNotifications(ctx, s.Database, each, daily)
	if err != nil {
		t.Fail("list due mail", err)
		return 0
	}
	t.SetPending(int64(len(due)))
	if !ok {
		return 0
	}
	pending := int64(len(due))
	sent := 0
	for _, d := range due {
		to, err := repo.StoreSiteOwnerEmail(ctx, s.Database, d.SiteID)
		if err != nil || !strings.Contains(to, "@") {
			continue
		}
		// Claimed before it is sent: another server (or the next tick after
		// a failed send) never sends the same email again.
		won, err := legacy.ClaimNotification(ctx, s.Database, d)
		if err != nil {
			t.Fail("claim mail", err)
			continue
		}
		if !won {
			continue
		}
		pending--
		t.SetPending(pending)
		t.Progress() // The claim durably consumes this batch, including failed sends.
		noun := "entries"
		if d.Count == 1 {
			noun = "entry"
		}
		subject := fmt.Sprintf("%d new %s in %s on %s", d.Count, noun, d.Name, d.SiteName)
		owner := s.ExportBase + "/"
		if d.Handle != "" {
			owner = s.MainSiteURL + "/" + d.Handle
		}
		stop := s.ExportBase + "/v1/data-notify/stop#t=" + url.QueryEscape(s.StopToken(d.SiteID, d.Name))
		text := fmt.Sprintf(`%d new %s arrived in %s on your site %s since %s.

See who sent them and what they say in your sites page: %s

Stop these emails for %s: %s

Simple Host
`, d.Count, noun, d.Name, d.SiteName, d.Since.UTC().Format("2 Jan 2006 15:04 UTC"), owner, d.Name, stop)
		if err := email.SendNoticeCtx(ctx, mailer, to, subject, text); err != nil {
			// Not retried: the next email counts from here, so these entries
			// are left out of the count rather than mailed twice.
			t.Fail("send mail", s.MailError(err))
			continue
		}
		sent++
		t.Progress()
	}
	if sent > 0 {
		log.Printf("submission emails: sent %d", sent)
	}
	return sent
}
func (s *StoreMail) FamilyNotice(ctx context.Context, f model.AddressFamily, subject, text string) bool {
	mailer, ok := s.Mailer.(email.NoticeSender)
	if !ok || !strings.Contains(f.Email, "@") {
		return false
	}
	if err := email.SendNoticeCtx(ctx, mailer, f.Email, subject, text); err != nil {
		log.Printf("address family: send notice: %v", err)
		return false
	}
	return true
}
