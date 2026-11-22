package data

// Gate checks the site-wide access gates in their existing order.
func (s *Service) Gate(call Call, write bool) error {
	if call.Owner {
		return nil
	}
	if call.Resource.SitePasscode == "inherit" && !call.PasscodeOK {
		return &Error{Code: "site_locked", Message: "unlock this site first"}
	}
	// A named-viewers site keeps its saved data to the owner and named viewers,
	// whatever the resource's site_passcode says.
	if !call.ViewersOK {
		return &Error{Code: "site_private", Message: "this site is open only to its named viewers; sign in on the site first"}
	}
	if write && !call.CSRFOK {
		return &Error{Code: "csrf_required", Message: "X-SH-CSRF: 1 required"}
	}
	return nil
}

func (s *Service) visitorActive(call *Call) *Error {
	if call.ActiveChecked {
		return nil
	}
	suspended, err := s.accounts.UserSuspended(call.Context, call.VisitorSessionUserID)
	if err != nil {
		return &Error{Code: "internal_error", Message: "internal server error"}
	}
	if suspended {
		return &Error{Code: "account_suspended"}
	}
	_ = s.accounts.TouchVisitorSession(call.Context, call.VisitorSessionID, s.Deps.VisitorSessionIdle)
	call.ActiveChecked = true
	return nil
}
