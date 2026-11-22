package access

import (
	"context"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/model"
)

// EmailChangeRefusal is why the account cannot change its sign-in email (a
// 403 *LifecycleError), nil when it can: the reviewer's account
// (reviewerEmail) and the PREVIEW_ACCOUNTS have theirs set by the operator,
// the admin's cannot change here, an account without an email address has
// none to confirm the change from, and an account that holds an event keeps
// its address.
func (s *Service) EmailChangeRefusal(ctx context.Context, user *model.User, reviewerEmail string, previewAccounts map[string]bool) error {
	refuse := func(code, msg string) error {
		return &LifecycleError{Status: http.StatusForbidden, Message: msg, Code: code}
	}
	switch {
	case reviewerEmail != "" && strings.EqualFold(user.Username, reviewerEmail):
		return refuse("reviewer_account", "this account's email is set by the operator")
	case IsOperator(user.IsAdmin, user.Username):
		return refuse("admin_account", "the admin account's sign-in email cannot be changed here")
	case previewAccounts[strings.ToLower(user.Username)]:
		return refuse("preview_account", "this account's email is set by the operator")
	case !strings.Contains(user.Username, "@"):
		return refuse("no_current_email", "this account does not sign in with an email address, so there is none to confirm the change from; write to support@simple-host.app")
	}
	t, err := s.EmailChangeTarget(ctx, user.ID)
	if err != nil {
		return err
	}
	if t.Event {
		return refuse("event_account", HoldingText("email"))
	}
	return nil
}
