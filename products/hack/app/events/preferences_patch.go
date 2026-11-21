package events

import (
	"context"

	model "github.com/vsriram/simple-host/products/hack/model"
)

// PreferencesPatch is a change to a person's Hack preferences: nil leaves a
// field as it is.
type PreferencesPatch struct {
	Theme                    *string
	OrganiserWalkthroughDone *bool
	JudgeWalkthroughDone     *bool
	EmailUpdates             *bool
}

var themes = map[string]bool{"system": true, "light": true, "dark": true}

func (s *Service) PatchPreferences(ctx context.Context, userID string, in PreferencesPatch) (model.HackPreferences, error) {
	if in.Theme != nil && !themes[*in.Theme] {
		return model.HackPreferences{}, &Error{Code: "invalid_theme", Message: "theme must be system, light or dark"}
	}
	p, err := s.HackPreferences(ctx, userID)
	if err != nil {
		return p, err
	}
	setIf(&p.Theme, in.Theme)
	setIf(&p.OrganiserWalkthroughDone, in.OrganiserWalkthroughDone)
	setIf(&p.JudgeWalkthroughDone, in.JudgeWalkthroughDone)
	setIf(&p.EmailUpdates, in.EmailUpdates)
	return p, s.SetHackPreferencesLookup(ctx, userID, p)
}

func setIf[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}

func AnnouncementText(title, body string) (string, string, error) {
	title, err := lineValue(title, "title", 1, 120)
	if err != nil {
		return "", "", err
	}
	body, err = textValue(body, "body", 1, 3000)
	return title, body, err
}

// AnnouncementChange checks an edit: a change that only sets pinned (no
// title and no body) pins or unpins without touching the text, and answers
// empty text.
func AnnouncementChange(title, body *string, pinned *bool) (string, string, error) {
	if pinned != nil && title == nil && body == nil {
		return "", "", nil
	}
	var t, b string
	setIf(&t, title)
	setIf(&b, body)
	return AnnouncementText(t, b)
}
