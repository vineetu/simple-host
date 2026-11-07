package hackevents

import (
	_ "embed"
	"encoding/json"
	"net/url"
	"strings"
)

// TEMP until merged with slice 1's navigation.go.
//
//go:embed navigation.json
var checklistNavigationTemporary []byte

type ChecklistTargetsTemporary struct{}

func (ChecklistTargetsTemporary) Resolve(targetID, slug string, params map[string]string) (ChecklistLink, bool) {
	var doc struct {
		Targets []struct {
			ID, Screen, Section string
			Tab                 *string
			HrefPattern         string `json:"href_pattern"`
		}
	}
	if json.Unmarshal(checklistNavigationTemporary, &doc) != nil {
		return ChecklistLink{}, false
	}
	for _, t := range doc.Targets {
		if t.ID == targetID {
			href := strings.ReplaceAll(t.HrefPattern, "{slug}", url.PathEscape(slug))
			for k, v := range params {
				href = strings.ReplaceAll(href, "{"+k+"}", url.QueryEscape(v))
			}
			return ChecklistLink{t.Screen, t.Section, t.Tab, href}, !strings.Contains(href, "{")
		}
	}
	return ChecklistLink{}, false
}
