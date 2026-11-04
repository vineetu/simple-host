package accounts

import (
	"context"
	"log"
	"strings"

	repo "github.com/vsriram/simple-host/internal/repository/accounts"
)

// DeliverSignInAlert applies account preferences and the daily browser/app claim before delivery.
func (s *Service) DeliverSignInAlert(ctx context.Context, userID, reviewerEmail, summary, app string, mailer interface {
	SendNotice(string, string, string) error
}, render func(email, handle string) (string, string)) {
	t, err := repo.GetSignInAlertTarget(ctx, s.database, userID)
	if err != nil {
		log.Printf("sign-in alert user_id=%s: %v", userID, err)
		return
	}
	if !t.On || t.Event || !strings.Contains(t.Email, "@") ||
		(reviewerEmail != "" && strings.EqualFold(t.Email, reviewerEmail)) {
		return
	}
	key := summary
	if app != "" {
		key = summary + " · " + app
	}
	first, err := repo.ClaimSignInAlert(ctx, s.database, userID, key)
	if err != nil || !first {
		if err != nil {
			log.Printf("sign-in alert user_id=%s: claim: %v", userID, err)
		}
		return
	}
	subject, text := render(t.Email, t.Handle)
	if err := mailer.SendNotice(t.Email, subject, text); err != nil {
		log.Printf("sign-in alert user_id=%s: send: %v", userID, err)
	}
}
