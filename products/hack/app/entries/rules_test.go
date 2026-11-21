package entries

import (
	"database/sql"
	"slices"
	"testing"
	"time"

	"github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/rules/hackentries"
)

func TestNextRules(t *testing.T) {
	str := func(s string) *string { return &s }
	ev := model.Event{EntryPreset: "video_or_website", EntryRules: hackentries.MarshalRules(hackentries.ExpandPreset("video_or_website")), JudgeFirst: "video"}
	for _, tc := range []struct {
		name                string
		req                 RulesUpdate
		preset, judge, code string
		errCode             string
	}{
		{"unchanged", RulesUpdate{}, "video_or_website", "video", "off", ""},
		{"preset sets its judge-first", RulesUpdate{Preset: str("video_website_code")}, "video_website_code", "site", "required", ""},
		{"explicit judge-first wins", RulesUpdate{Preset: str("video_or_website"), JudgeFirst: str("site")}, "video_or_website", "site", "off", ""},
		{"same-as-preset field keeps preset", RulesUpdate{Fields: map[string]string{"code_url": "off"}}, "video_or_website", "video", "off", ""},
		{"differing field demotes to custom", RulesUpdate{Fields: map[string]string{"code_url": "required"}}, "custom", "video", "required", ""},
		{"unknown field", RulesUpdate{Fields: map[string]string{"colour": "off"}}, "", "", "", "invalid_entry_field"},
		{"one_of on a summary field", RulesUpdate{Fields: map[string]string{"title": "one_of"}}, "", "", "", "invalid_entry_field_mode"},
	} {
		preset, fields, judge, err := nextRules(ev, tc.req)
		if code := errCode(t, err); code != tc.errCode || code == "" && (preset != tc.preset || judge != tc.judge || fields["code_url"] != tc.code) {
			t.Errorf("%s: got %q %q code=%q err=%v", tc.name, preset, judge, fields["code_url"], err)
		}
	}
}

func TestProgressSteps(t *testing.T) {
	fields := hackentries.ExpandPreset("video_or_website")
	full := model.EventEntry{Title: "T", Problem: "P", Tagline: "Tag", Tools: []string{"Go"}, Maturity: "working", VideoURL: "https://example.com/v"}
	submitted := full
	submitted.SubmittedAt = sql.NullTime{Time: time.Now(), Valid: true}
	for _, tc := range []struct {
		name       string
		e          model.EventEntry
		siteLive   bool
		filled, of int
		evidence   []string
		next       string
	}{
		{"empty entry", model.EventEntry{}, false, 0, 5, []string{"one_of"}, "summary"},
		{"summary done, no evidence", model.EventEntry{Title: "T", Problem: "P", Tagline: "Tag", Tools: []string{"Go"}, Maturity: "working"}, false, 5, 5, []string{"one_of"}, "evidence"},
		{"live site is evidence", model.EventEntry{Title: "T", Problem: "P", Tagline: "Tag", Tools: []string{"Go"}, Maturity: "working"}, true, 5, 5, []string{}, "submit"},
		{"ready to submit", full, false, 5, 5, []string{}, "submit"},
		{"submitted", submitted, false, 5, 5, []string{}, ""},
	} {
		p := progressSteps(fields, tc.e, tc.siteLive, false, nil)
		if p.Filled != tc.filled || p.Of != tc.of || !slices.Equal(p.EvidenceMissing, tc.evidence) || p.Next != tc.next || p.EvidenceDone != (len(tc.evidence) == 0) {
			t.Errorf("%s: got %d/%d evidence=%v next=%q", tc.name, p.Filled, p.Of, p.EvidenceMissing, p.Next)
		}
	}
}
