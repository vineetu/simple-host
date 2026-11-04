package storage

import (
	"github.com/vsriram/simple-host/internal/db"
	rules "github.com/vsriram/simple-host/internal/rules/storage"
)

// Gate checks the site-wide access gates in their existing order.
func (s *Service) Gate(call Call, write bool) error {
	if call.Owner {
		return nil
	}
	if call.Resource.SitePasscode == "inherit" && !s.deps.PasscodeAllows() {
		return &Error{Code: "site_locked", Message: "unlock this site first"}
	}
	// A named-viewers site keeps its saved data to the owner and named viewers,
	// whatever the resource's site_passcode says.
	if !s.deps.ViewersAllow() {
		return &Error{Code: "site_private", Message: "this site is open only to its named viewers; sign in on the site first"}
	}
	if write && !call.CSRFOK {
		return &Error{Code: "csrf_required", Message: "X-SH-CSRF: 1 required"}
	}
	return nil
}

// checkedAllow decides whether a checked visitor may post.
func (s *Service) checkedAllow(call *Call) *Error {
	if call.SpamCheck == "ok" {
		call.CheckedPost = true
		return nil
	}
	code := call.SpamCheck
	if code == "" {
		code = "spam_check_required"
	}
	return &Error{Code: code}
}

func (s *Service) visitorActive(call *Call) *Error {
	if call.ActiveChecked {
		return nil
	}
	suspended, err := db.UserSuspended(call.Context, s.db, call.VisitorSessionUserID)
	if err != nil {
		return &Error{Code: "internal_error", Message: "internal server error"}
	}
	if suspended {
		return &Error{Code: "account_suspended"}
	}
	_ = db.TouchVisitorSession(call.Context, s.db, call.VisitorSessionID, s.deps.VisitorSessionIdle)
	call.ActiveChecked = true
	return nil
}

// allowDecisions resolves an action and retains refusals in their original order.
func (s *Service) allowDecisions(call *Call, action, value string) (string, bool, []*Error) {
	var refusals []*Error
	active := func() bool {
		if err := s.visitorActive(call); err != nil {
			refusals = append(refusals, err)
			return false
		}
		return true
	}
	if call.Owner {
		return "", true, nil
	}
	switch value {
	case rules.WhoAnyone:
		if call.VisitorAI && (action == "edit" || action == "delete") {
			return call.VisitorID, true, nil
		}
		return "", true, nil
	case rules.WhoChecked:
		if call.VisitorAI && active() {
			return "", true, refusals
		}
		if call.OwnerOnSite && active() {
			return "", true, refusals
		}
		if call.OwnerOnSite {
			return "", false, refusals
		}
		if call.VisitorID != "" && !active() {
			return "", false, refusals
		}
		if err := s.checkedAllow(call); err != nil {
			return "", false, append(refusals, err)
		}
		return "", true, refusals
	case rules.WhoOwn, rules.WhoSignedIn:
		if call.VisitorID == "" {
			return "", false, []*Error{{Code: "sign_in_required", Message: "sign in to this site first"}}
		}
		if !active() {
			return "", false, refusals
		}
		if call.VisitorAI && (action == "edit" || action == "delete") {
			return call.VisitorID, true, refusals
		}
		if call.OwnerOnSite || value == rules.WhoSignedIn {
			return "", true, refusals
		}
		return call.VisitorID, true, refusals
	case rules.WhoOwner:
		if call.OwnerOnSite && active() {
			return "", true, refusals
		}
		if call.OwnerOnSite {
			return "", false, refusals
		}
	}
	if (action == "edit" || action == "delete") && value != rules.WhoNobody {
		if add := call.CurrentMatrix.Add; add == rules.WhoAnyone || add == rules.WhoChecked || add == rules.WhoSignedIn && call.VisitorID != "" {
			return "", false, []*Error{{Code: "add_only", Message: "visitors can add here, not change or delete"}}
		}
	}
	return "", false, []*Error{{Code: "forbidden", Message: action + " is not open to this visitor here (" + value + ")"}}
}

// Allow resolves an action, retaining any ordered refusals from session checks.
func (s *Service) Allow(call *Call, action, value string) (string, error) {
	filter, allowed, refusals := s.allowDecisions(call, action, value)
	if len(refusals) == 0 {
		return filter, nil
	}
	return filter, &Error{Code: refusals[0].Code, Message: refusals[0].Message, Allowed: allowed, Refusals: refusals}
}

// Access applies the site gate and the resource matrix in order.
func (s *Service) Access(call *Call, action string) (string, error) {
	if err := s.Gate(*call, action != "read"); err != nil {
		return "", err
	}
	call.CurrentMatrix = call.Resource.Matrix()
	return s.Allow(call, action, call.CurrentMatrix.Value(action))
}
