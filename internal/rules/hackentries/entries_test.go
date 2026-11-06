package hackentries

import (
	"github.com/vsriram/simple-host/internal/model"
	"slices"
	"testing"
)

func TestEvaluateEntryPresets(t *testing.T) {
	full := model.EventEntry{
		Title: "T", Problem: "P", Tagline: "Tag", Audience: "Aud",
		Tools: []string{"Go"}, Maturity: "working", VideoURL: "https://example.com/v",
	}
	track := &model.EventTrack{ID: "trk1", Slug: "ai", Name: "AI"}

	cases := []struct {
		name      string
		preset    string
		e         model.EventEntry
		siteLive  bool
		hasTracks bool
		track     *model.EventTrack
		complete  bool
		missing   []string
	}{
		{"build complete via video", "build", full, false, false, nil, true, nil},
		{"build missing everything", "build", model.EventEntry{}, false, false, nil, false,
			[]string{"title", "problem", "tagline", "audience", "tools", "maturity", "one_of"}},
		{"build needs a track when the event has one", "build", full, false, true, nil, false, []string{"track"}},
		{"build satisfied once a track is chosen", "build", full, false, true, track, true, nil},
		{"build one_of satisfied by site instead of video", "build",
			model.EventEntry{Title: "T", Problem: "P", Tagline: "Tag", Audience: "Aud", Tools: []string{"Go"}, Maturity: "working"},
			true, false, nil, true, nil},
		{"build one_of unmet: no video, site not live", "build",
			model.EventEntry{Title: "T", Problem: "P", Tagline: "Tag", Audience: "Aud", Tools: []string{"Go"}, Maturity: "working"},
			false, false, nil, false, []string{"one_of"}},
		{"code requires code_url, audience optional", "code",
			model.EventEntry{Title: "T", Problem: "P", Tagline: "Tag", Tools: []string{"Go"}, Maturity: "working", CodeURL: "https://example.com/c", VideoURL: "https://example.com/v"},
			false, false, nil, true, nil},
		{"code missing code_url and the one_of group", "code",
			model.EventEntry{Title: "T", Problem: "P", Tagline: "Tag", Tools: []string{"Go"}, Maturity: "working"},
			false, false, nil, false, []string{"code_url", "one_of"}},
		{"ideas: tools and maturity optional, screenshot in the one_of group", "ideas",
			model.EventEntry{Title: "T", Problem: "P", Tagline: "Tag", Audience: "Aud", HasScreenshot: true},
			false, false, nil, true, nil},
		{"ideas missing required and the one_of group", "ideas",
			model.EventEntry{Title: "T"}, false, false, nil, false,
			[]string{"problem", "tagline", "audience", "one_of"}},
		{"title floor: empty title is always missing even under a lenient preset", "ideas",
			model.EventEntry{Problem: "P", Tagline: "Tag", Audience: "Aud", HasScreenshot: true},
			false, false, nil, false, []string{"title"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fields := ExpandPreset(c.preset)
			got := Evaluate(fields, c.e, c.siteLive, c.hasTracks, c.track)
			if got.Complete != c.complete {
				t.Fatalf("complete = %v, want %v (missing %v)", got.Complete, c.complete, got.Missing)
			}
			if !slices.Equal(got.Missing, c.missing) && !(len(got.Missing) == 0 && len(c.missing) == 0) {
				t.Fatalf("missing = %v, want %v", got.Missing, c.missing)
			}
		})
	}
}

// TestEvaluateEntryOneOfGroupSizes: an empty group never adds "one_of"; a
// group of exactly one field behaves like that field being required; a
// group of several is satisfied by any one of them.
func TestEvaluateEntryOneOfGroupSizes(t *testing.T) {
	empty := map[string]string{"title": "required"}
	if got := Evaluate(empty, model.EventEntry{Title: "T"}, false, false, nil); !got.Complete {
		t.Fatalf("no one_of group configured should never block: %v", got.Missing)
	}

	one := map[string]string{"title": "required", "video_url": "one_of"}
	if got := Evaluate(one, model.EventEntry{Title: "T"}, false, false, nil); got.Complete || !slices.Contains(got.Missing, "one_of") {
		t.Fatalf("single-member one_of unmet: %v", got.Missing)
	}
	if got := Evaluate(one, model.EventEntry{Title: "T", VideoURL: "https://example.com/v"}, false, false, nil); !got.Complete {
		t.Fatalf("single-member one_of met: %v", got.Missing)
	}

	two := map[string]string{"title": "required", "video_url": "one_of", "site": "one_of"}
	if got := Evaluate(two, model.EventEntry{Title: "T"}, true, false, nil); !got.Complete {
		t.Fatalf("two-member one_of met by site alone: %v", got.Missing)
	}
	if got := Evaluate(two, model.EventEntry{Title: "T", VideoURL: "https://example.com/v"}, false, false, nil); !got.Complete {
		t.Fatalf("two-member one_of met by video alone: %v", got.Missing)
	}
	if got := Evaluate(two, model.EventEntry{Title: "T"}, false, false, nil); got.Complete {
		t.Fatalf("two-member one_of should still block when neither is present")
	}
}

// TestEntryRulesSummary: the one sentence organisers and participants read,
// built server-side so every surface words it identically (section 3).
func TestEntryRulesSummary(t *testing.T) {
	if got := RulesSummary(ExpandPreset("build")); got != "a video or a published website" {
		t.Fatalf("build summary = %q", got)
	}
	if got := RulesSummary(ExpandPreset("code")); got != "your code link, and a video or your website" {
		t.Fatalf("code summary = %q", got)
	}
	if got := RulesSummary(ExpandPreset("ideas")); got != "a video or a published website or a screenshot" {
		t.Fatalf("ideas summary = %q", got)
	}
	// SIM-382's two current presets.
	if got := RulesSummary(ExpandPreset("video_or_website")); got != "a video or a published website" {
		t.Fatalf("video_or_website summary = %q", got)
	}
	// video_url, site and code_url are all "required", not "one_of", so
	// there is no one_of group to name -- only the code link reads.
	if got := RulesSummary(ExpandPreset("video_website_code")); got != "your code link" {
		t.Fatalf("video_website_code summary = %q", got)
	}
	// Every evidence field off: nothing to show.
	off := NormaliseRules(map[string]string{"video_url": "off", "site": "off", "code_url": "off", "screenshot": "off"})
	if got := RulesSummary(off); got != "" {
		t.Fatalf("no evidence summary = %q", got)
	}
}

// TestEntryMissingMessageCanonicalOrder: missing fields read in
// RuleFields order, with "one_of" (naming the event's own group) last.
func TestEntryMissingMessageCanonicalOrder(t *testing.T) {
	fields := ExpandPreset("ideas") // title, problem, tagline, audience required; one_of group set
	check := Evaluate(fields, model.EventEntry{}, false, true, nil)
	if !slices.Equal(check.Missing, []string{"title", "problem", "tagline", "audience", "track", "one_of"}) {
		t.Fatalf("canonical order: %v", check.Missing)
	}
	msg := MissingMessage(check.Missing, fields)
	want := "a title, the problem it solves, the one-line solution, who it's for, a track, and a video or a published website or a screenshot"
	if msg != want {
		t.Fatalf("message = %q, want %q", msg, want)
	}
}

// TestNormaliseEntryRulesDropsBad: an unknown field, an unknown mode, and
// one_of on a field that may not carry it are all dropped to "optional";
// title is always forced to "required" regardless of input.
func TestNormaliseEntryRulesDropsBad(t *testing.T) {
	in := map[string]string{
		"title":     "optional", // forced back to required
		"problem":   "one_of",   // not an OneOfFields member: dropped to optional
		"tagline":   "nonsense", // unknown mode: dropped to optional
		"video_url": "one_of",   // allowed
		"nope":      "required", // unknown field: ignored entirely
	}
	out := NormaliseRules(in)
	if out["title"] != "required" {
		t.Fatalf("title = %q", out["title"])
	}
	if out["problem"] != "optional" {
		t.Fatalf("problem (bad one_of) = %q", out["problem"])
	}
	if out["tagline"] != "optional" {
		t.Fatalf("tagline (bad mode) = %q", out["tagline"])
	}
	if out["video_url"] != "one_of" {
		t.Fatalf("video_url (valid one_of) = %q", out["video_url"])
	}
	if _, ok := out["nope"]; ok {
		t.Fatalf("unknown field leaked into output: %v", out)
	}
	// Every known field is present (defaults to optional) even when the
	// input map is nil or empty.
	nilOut := NormaliseRules(nil)
	for _, f := range RuleFields {
		if _, ok := nilOut[f]; !ok {
			t.Fatalf("field %s missing from a nil-input normalise: %v", f, nilOut)
		}
	}
	if nilOut["title"] != "required" {
		t.Fatalf("nil input title = %q", nilOut["title"])
	}
}

// TestEvaluateEntryCurrentPresets: SIM-382's two presets -- video_or_website
// (the default, video/site one_of, code off) and video_website_code (all
// three required, not one_of).
func TestEvaluateEntryCurrentPresets(t *testing.T) {
	full := model.EventEntry{
		Title: "T", Problem: "P", Tagline: "Tag", Audience: "Aud",
		Tools: []string{"Go"}, Maturity: "working", VideoURL: "https://example.com/v",
	}
	cases := []struct {
		name     string
		preset   string
		e        model.EventEntry
		siteLive bool
		complete bool
		missing  []string
	}{
		{"video_or_website complete via video", "video_or_website", full, false, true, nil},
		{"video_or_website one_of satisfied by site instead of video", "video_or_website",
			model.EventEntry{Title: "T", Problem: "P", Tagline: "Tag", Audience: "Aud", Tools: []string{"Go"}, Maturity: "working"},
			true, true, nil},
		{"video_or_website one_of unmet: no video, no live site", "video_or_website",
			model.EventEntry{Title: "T", Problem: "P", Tagline: "Tag", Audience: "Aud", Tools: []string{"Go"}, Maturity: "working"},
			false, false, []string{"one_of"}},
		{"video_website_code: video alone is not enough, code and site also required", "video_website_code",
			full, false, false, []string{"site", "code_url"}},
		{"video_website_code: all three present, site live", "video_website_code",
			model.EventEntry{Title: "T", Problem: "P", Tagline: "Tag", Audience: "Aud", Tools: []string{"Go"}, Maturity: "working", VideoURL: "https://example.com/v", CodeURL: "https://example.com/c"},
			true, true, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fields := ExpandPreset(c.preset)
			got := Evaluate(fields, c.e, c.siteLive, false, nil)
			if got.Complete != c.complete {
				t.Fatalf("complete = %v, want %v (missing %v)", got.Complete, c.complete, got.Missing)
			}
			if !slices.Equal(got.Missing, c.missing) && !(len(got.Missing) == 0 && len(c.missing) == 0) {
				t.Fatalf("missing = %v, want %v", got.Missing, c.missing)
			}
		})
	}
}

// TestPresetSettable: only the two current presets may be set going
// forward; a legacy preset (still a valid stored/evaluated value via
// PresetKnown) is not settable (SIM-382).
func TestPresetSettable(t *testing.T) {
	for _, p := range []string{"video_or_website", "video_website_code"} {
		if !PresetSettable(p) {
			t.Fatalf("%s should be settable", p)
		}
		if !PresetKnown(p) {
			t.Fatalf("%s should be known", p)
		}
	}
	for _, p := range []string{"build", "code", "ideas", "custom", "nonsense"} {
		if PresetSettable(p) {
			t.Fatalf("%s should not be settable", p)
		}
	}
	for _, p := range []string{"build", "code", "ideas", "custom"} {
		if !PresetKnown(p) {
			t.Fatalf("%s should still be a known (legacy) stored value", p)
		}
	}
	if PresetKnown("nonsense") {
		t.Fatalf("nonsense should not be known")
	}
}
