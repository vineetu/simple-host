package notify

import (
	"context"
	"log"
	"strings"

	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/model"
)

type StoreMail struct {
	Mailer any
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
