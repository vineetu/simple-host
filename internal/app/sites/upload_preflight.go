package sites

import (
	"context"
	"errors"
	"strings"

	"github.com/vsriram/simple-host/internal/model"
	rules "github.com/vsriram/simple-host/internal/rules/sites"
)

// UpdatePreflightInput describes an upload before its body is read.
type UpdatePreflightInput struct {
	User                  *model.User
	Name, Publish, Create string
}

// UpdatePreflightResult selects the existing-site or first-deploy workflow.
type UpdatePreflightResult struct{ Publish, Create bool }

// UpdatePreflight validates the target and options before checking creation eligibility.
// Reserved names remain updateable when the account already owns the site.
func (s *Service) UpdatePreflight(ctx context.Context, in UpdatePreflightInput) (out UpdatePreflightResult, err error) {
	if err := rules.ValidateShape(in.Name); err != nil {
		return out, &Error{Code: "invalid_name", Message: err.Error()}
	}
	switch strings.ToLower(strings.TrimSpace(in.Publish)) {
	case "", "true", "1", "yes":
		out.Publish = true
	case "false", "0", "no":
	default:
		return out, &Error{Code: "invalid_request", Message: "publish must be true or false"}
	}
	switch in.Create {
	case "", "0", "false":
		return out, nil
	case "1", "true":
	default:
		return out, &Error{Code: "invalid_request", Message: "create must be 1 or 0"}
	}
	err = s.CreatePreflight(ctx, PreflightInput{User: in.User, Name: in.Name, CheckExisting: true, Publish: in.Publish})
	var e *Error
	if errors.As(err, &e) && e.Code == "preflight_exists" {
		return out, nil
	}
	out.Create = err == nil
	return out, err
}
