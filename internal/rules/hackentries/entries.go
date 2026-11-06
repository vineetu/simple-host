package hackentries

import (
	"encoding/json"
	"github.com/vsriram/simple-host/internal/model"
	"strings"
)

// RuleFields are every field the rules engine understands, in canonical
// order. missing ids, the CSV "missing" cell and the organiser's rule rows
// all follow this order.
var RuleFields = []string{
	"title", "problem", "tagline", "audience", "track", "tools", "maturity",
	"description", "video_url", "site", "code_url", "screenshot",
}

// OneOfFields are the only fields a rules JSON may mark "one_of".
var OneOfFields = map[string]bool{"video_url": true, "site": true, "code_url": true, "screenshot": true}

// RuleModeOK lists the supported field modes.
var RuleModeOK = map[string]bool{"required": true, "optional": true, "off": true, "one_of": true}

// EntryRules is events.entry_rules, version 1.
type EntryRules struct {
	V      int               `json:"v"`
	Fields map[string]string `json:"fields"`
}

// Presets is the Go table PUT .../entry-rules {preset} expands, and what a
// newly created event starts on ("video_or_website"). SIM-382 (owner,
// 2026-11-05/06) narrowed the organiser's choice to exactly two presets,
// both naming what teams hand in:
//   - Video or website (the default): everything on the card Required,
//     Video and Website One of, Code Off, Screenshot Optional, video first.
//   - Video, website and code link: everything on the card Required,
//     Video, Website and Code link all Required (not One of), Screenshot
//     Optional, website first.
//
// Description is Optional in both. Title is always Required, and Track is
// ignored when the event has no tracks.
//
// "build", "code" and "ideas" (AI build, Coding, Ideas and design) stay
// here, unreachable from PresetSettable: an event already on one of them
// (or on "custom") keeps working exactly as before -- LoadRules/ExpandPreset
// still expand it -- but PutRules refuses to set any of the four again
// (section below), so the website and the connector only ever offer the
// two current presets.
var Presets = map[string]map[string]string{
	"video_or_website": {
		"title": "required", "problem": "required", "tagline": "required",
		"audience": "required", "track": "required", "tools": "required", "maturity": "required",
		"description": "optional", "video_url": "one_of", "site": "one_of", "code_url": "off", "screenshot": "optional",
	},
	"video_website_code": {
		"title": "required", "problem": "required", "tagline": "required",
		"audience": "required", "track": "required", "tools": "required", "maturity": "required",
		"description": "optional", "video_url": "required", "site": "required", "code_url": "required", "screenshot": "optional",
	},
	// Legacy, read-only from PutRules's point of view (see PresetSettable):
	"build": {
		"title": "required", "problem": "required", "tagline": "required",
		"audience": "required", "track": "required", "tools": "required", "maturity": "required",
		"description": "optional", "video_url": "one_of", "site": "one_of", "code_url": "off", "screenshot": "optional",
	},
	"code": {
		"title": "required", "problem": "required", "tagline": "required",
		"audience": "optional", "track": "required", "tools": "required", "maturity": "required",
		"description": "optional", "video_url": "one_of", "site": "one_of", "code_url": "required", "screenshot": "optional",
	},
	"ideas": {
		"title": "required", "problem": "required", "tagline": "required",
		"audience": "required", "track": "required", "tools": "optional", "maturity": "optional",
		"description": "optional", "video_url": "one_of", "site": "one_of", "code_url": "off", "screenshot": "one_of",
	},
}

// PresetJudgeFirst is which evidence judges see first for each preset,
// applied when an organiser picks the preset without naming judge_first.
var PresetJudgeFirst = map[string]string{
	"video_or_website": "video", "video_website_code": "site",
	"build": "video", "code": "site", "ideas": "video",
}

// PresetOrder is the presets an organiser can pick, in display order.
var PresetOrder = []string{"video_or_website", "video_website_code"}

// PresetKnown reports whether a preset name is a supported stored value:
// the two current presets, a legacy one an older event may still carry, or
// "custom". Use this for reading/evaluating a stored event; use
// PresetSettable to decide whether a PUT may set it.
func PresetKnown(preset string) bool {
	_, ok := Presets[preset]
	return ok || preset == "custom"
}

// PresetSettable reports whether an organiser (or the connector) may set
// this preset going forward: only the two current ones. "build", "code",
// "ideas" and "custom" are refused by PutRules with a message naming the
// two (SIM-382) -- an event already on one of them keeps it until the
// organiser switches to a current preset or edits a field, which demotes
// it to "custom" as before.
func PresetSettable(preset string) bool {
	return preset == "video_or_website" || preset == "video_website_code"
}

// ExpandPreset is Presets[preset] normalised to every field, title
// forced required. An unknown preset (including "custom") is every field
// optional, title required -- the caller then applies its own fields on top.
func ExpandPreset(preset string) map[string]string {
	return NormaliseRules(Presets[preset])
}

// NormaliseRules fills every field (a missing key means "optional"),
// drops an unknown field or an unknown mode, drops "one_of" on a field that
// may not carry it, and forces title to "required". entry_rules JSON is
// validated server-side and never stored as the client sent it (section
// 9.6): this is the one function that enforces that.
func NormaliseRules(fields map[string]string) map[string]string {
	out := make(map[string]string, len(RuleFields))
	for _, f := range RuleFields {
		mode := "optional"
		if fields != nil {
			if m, ok := fields[f]; ok && RuleModeOK[m] && (m != "one_of" || OneOfFields[f]) {
				mode = m
			}
		}
		out[f] = mode
	}
	out["title"] = "required"
	return out
}

// MarshalRules is the normalised fields as entry_rules JSON (version 1).
func MarshalRules(fields map[string]string) []byte {
	raw, err := json.Marshal(EntryRules{V: 1, Fields: NormaliseRules(fields)})
	if err != nil {
		// Fields are plain strings; this cannot fail in practice.
		return []byte(`{"v":1,"fields":{}}`)
	}
	return raw
}

// LoadRules parses an event's stored entry_rules, defaulting to every
// field optional (title required) on a blank value -- a fresh row before a
// migration backfill runs, or on an instance that predates this release.
func LoadRules(raw []byte) map[string]string {
	var r EntryRules
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &r)
	}
	return NormaliseRules(r.Fields)
}

// OneOfGroup is the fields marked "one_of", in canonical order. Exactly
// one such group can exist (every field in OneOfFields marked one_of
// together makes the group); this just reads it back.
func OneOfGroup(fields map[string]string) []string {
	var out []string
	for _, f := range RuleFields {
		if OneOfFields[f] && fields[f] == "one_of" {
			out = append(out, f)
		}
	}
	return out
}

// Check is whether an entry is complete, and canonically-ordered reasons
// why not: field ids, plus "one_of" for an unmet one-of group, at the end.
type Check struct {
	Complete bool
	Missing  []string
}

// FieldPresent is whether one field of the entry/team counts as filled,
// for completeness. site comes from siteLive (the team's actual site state),
// never from the entry row (section 4.1: "the website evidence comes from
// sites, never from the entry row"). track comes from teamTrack, the team's
// chosen track, not anything stored on the entry.
func FieldPresent(field string, e model.EventEntry, siteLive bool, teamTrack *model.EventTrack) bool {
	switch field {
	case "title":
		return e.Title != ""
	case "problem":
		return e.Problem != ""
	case "tagline":
		return e.Tagline != ""
	case "audience":
		return e.Audience != ""
	case "track":
		return teamTrack != nil
	case "tools":
		return len(e.Tools) > 0
	case "maturity":
		return e.Maturity != ""
	case "description":
		return e.Description != ""
	case "video_url":
		return e.VideoURL != ""
	case "site":
		return siteLive
	case "code_url":
		return e.CodeURL != ""
	case "screenshot":
		return e.HasScreenshot
	default:
		return false
	}
}

// Evaluate is the only place that answers "is this entry complete"
// (section 3): entryCompleteness, queueReceipt, the CSV, listEntries, the
// judge queue, the Home progress and the settle sweep all go through this.
// hasTracks false ignores the "track" field even when the rules ask for it
// ("with no tracks in the event, track is ignored").
func Evaluate(fields map[string]string, e model.EventEntry, siteLive, hasTracks bool, teamTrack *model.EventTrack) Check {
	missing := []string{} // never null in JSON
	for _, f := range RuleFields {
		if f == "track" && !hasTracks {
			continue
		}
		if fields[f] == "required" && !FieldPresent(f, e, siteLive, teamTrack) {
			missing = append(missing, f)
		}
	}
	if group := OneOfGroup(fields); len(group) > 0 {
		met := false
		for _, f := range group {
			if FieldPresent(f, e, siteLive, teamTrack) {
				met = true
				break
			}
		}
		if !met {
			missing = append(missing, "one_of")
		}
	}
	return Check{Complete: len(missing) == 0, Missing: missing}
}

// OneOfWord is how a one-of field reads in the rules sentence.
// codeNamed is true once code_url is already named as required, which reads
// "your website" there instead of "a published website" alone.
func OneOfWord(field string, codeNamed bool) string {
	switch field {
	case "video_url":
		return "a video"
	case "site":
		if codeNamed {
			return "your website"
		}
		return "a published website"
	case "code_url":
		return "your code"
	case "screenshot":
		return "a screenshot"
	default:
		return field
	}
}

// RulesSummary is the one sentence naming what "show your work" needs
// ("a video or a published website", or "your code link, and a video or
// your website"), built server-side so the website and the AI apps word it
// identically (section 3). It only describes the show-your-work fields
// (code_url and the one-of group); the summary-card fields (problem,
// audience, track, tools, maturity) are not part of it.
func RulesSummary(fields map[string]string) string {
	var parts []string
	codeNamed := fields["code_url"] == "required"
	if codeNamed {
		parts = append(parts, "your code link")
	}
	if group := OneOfGroup(fields); len(group) > 0 {
		words := make([]string, 0, len(group))
		for _, f := range group {
			words = append(words, OneOfWord(f, codeNamed))
		}
		parts = append(parts, strings.Join(words, " or "))
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
	}
}

// SummaryFields are the summary-card lines progress.steps["summary"]
// counts: problem, tagline, audience, track, tools, maturity, plus title
// when it is empty (section 4.3).
var SummaryFields = []string{"title", "problem", "tagline", "audience", "track", "tools", "maturity"}

// FieldWords names a field in plain words, for the "missing" list a
// client renders and for progress.steps["summary"].missing. one_of itself
// is reworded per-event by MissingMessage (the actual group members),
// this is only the generic fallback.
var FieldWords = map[string]string{
	"title": "a title", "problem": "the problem it solves", "tagline": "the one-line solution",
	"audience": "who it's for", "track": "a track", "tools": "the tools used",
	"maturity": "how far along it is", "description": "a description",
	"video_url": "a video", "site": "a published website", "code_url": "your code link",
	"screenshot": "a screenshot", "one_of": "a video or a published website",
}

// MissingMessage renders a missing list (field ids, plus "one_of") as
// one plain-English clause: "who it's for, and a video or a published
// website". "one_of" names the event's own one-of group.
func MissingMessage(missing []string, fields map[string]string) string {
	words := make([]string, 0, len(missing))
	for _, m := range missing {
		if m == "one_of" {
			if group := OneOfGroup(fields); len(group) > 0 {
				parts := make([]string, 0, len(group))
				for _, f := range group {
					parts = append(parts, OneOfWord(f, false))
				}
				words = append(words, strings.Join(parts, " or "))
				continue
			}
		}
		if w, ok := FieldWords[m]; ok {
			words = append(words, w)
		} else {
			words = append(words, m)
		}
	}
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	case 2:
		return words[0] + " and " + words[1]
	default:
		return strings.Join(words[:len(words)-1], ", ") + ", and " + words[len(words)-1]
	}
}

// RulesEditable: rules may change from draft through building. From
// closed onwards they are read-only (409 entry_rules_closed), since judging
// has to see a fixed definition of complete.
func RulesEditable(stage string) bool {
	return stage == "draft" || stage == "open" || stage == "building"
}

// ModeSuffix describes the additional mode allowed for a field.
func ModeSuffix(field string) string {
	if OneOfFields[field] {
		return " or one_of"
	}
	return ""
}

// FieldsEqual reports whether two field maps have the same entries.
func FieldsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// JoinNames joins names for display.
func JoinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}
