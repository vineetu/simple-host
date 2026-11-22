package entries

import (
	"slices"
	"testing"

	hacktest "github.com/vsriram/simple-host/products/hack/hacktest"
)

// Entry rules (slice 1 of the entries and participant journey package,
// build-plan.md): the rules engine (hack_entry_rules.go), the new PUT entry
// fields and submit/unsubmit, the deadline settle sweep, judge eligibility
// over a settled-incomplete or hand-excluded entry, the rules PUT (presets,
// dry run, the deprecated entry_required alias) and the entries CSV/counts.
//
// The harness is newHackApp (DB_DSN); see hack_test.go and
// newHackEntryWorld (hack_entries_test.go) for the pattern this file follows.

// --- HTTP tests: a small world builder, separate from newHackEntryWorld --

// --- PUT entry: new field limits ------------------------------------------

// --- Draft writes never check rules; a submitted save that would become
// incomplete is refused and changes nothing. --------------------------------

func TestHackEntryDraftNeverChecksRulesSubmittedCannotRegress(t *testing.T) {
	w := newRulesWorld(t, 1)
	a, p := w.a, w.Teams[0].Person

	// A draft save that leaves the entry wildly incomplete is still 200.
	r := a.At(t, "PUT", w.EntryPath(), map[string]string{"title": "Only a title"}, a.Key(p))
	if r.Status != 200 {
		t.Fatalf("draft incomplete save: %d %s", r.Status, r.Body)
	}
	if r.JSON(t)["complete"] != false {
		t.Fatalf("draft should read incomplete: %#v", r.JSON(t))
	}

	// Complete it and submit.
	if r := a.At(t, "PUT", w.EntryPath(), hacktest.CompleteFields(), a.Key(p)); r.Status != 200 {
		t.Fatalf("complete: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "POST", "/v1/hack/events/"+w.Slug+"/entry/submit", map[string]any{}, a.Key(p))
	if r.Status != 200 || r.JSON(t)["status"] != "submitted" {
		t.Fatalf("submit: %d %s", r.Status, r.Body)
	}

	// Removing a required line on a submitted entry is refused, unchanged.
	r = a.At(t, "PUT", w.EntryPath(), map[string]string{"problem": ""}, a.Key(p))
	hacktest.WantCode(t, "regress submitted", r, 422, "entry_would_be_incomplete")
	missing := hacktest.JSONStrings(t, r.JSON(t)["missing"])
	if !slices.Equal(missing, []string{"problem"}) {
		t.Fatalf("missing = %v", missing)
	}
	after := a.At(t, "GET", w.EntryPath(), nil, a.Key(p)).JSON(t)
	if after["status"] != "submitted" || after["entry"].(map[string]any)["problem"] != "A problem" {
		t.Fatalf("rejected write changed the entry: %#v", after)
	}
}

// --- Submit / unsubmit ------------------------------------------------------

// --- The deadline settle sweep ---------------------------------------------

// --- Judge eligibility over settled-incomplete and hand-excluded entries --

// --- Rules PUT: presets, overrides, dry run, closed, the deprecated alias --

func TestHackEntryRulesPresetsAndDryRun(t *testing.T) {
	w := newRulesWorld(t, 3)
	a := w.a
	base := "/v1/hack/events/" + w.Slug + "/entry-rules"

	// A new event starts on the video_or_website preset, expanded.
	view := a.At(t, "GET", base, nil, a.Key(w.Org)).JSON(t)
	fields := view["fields"].(map[string]any)
	if view["preset"] != "video_or_website" || fields["audience"] != "optional" || fields["code_url"] != "off" {
		t.Fatalf("default video_or_website preset: %#v", view)
	}

	// SIM-382: a removed preset (the three retired names, and custom) is
	// refused when set directly, naming the two current ones.
	for _, bad := range []string{"build", "code", "ideas", "custom"} {
		r := a.At(t, "PUT", base, map[string]any{"preset": bad}, a.Key(w.Org))
		if r.Status != 400 || r.JSON(t)["code"] != "invalid_preset" {
			t.Fatalf("preset %s should be refused: %d %s", bad, r.Status, r.Body)
		}
	}

	// Switching preset to "video_website_code" expands its own table: all
	// three show-your-work lines required, not one_of.
	codeR := a.At(t, "PUT", base, map[string]any{"preset": "video_website_code"}, a.Key(w.Org))
	if codeR.Status != 200 {
		t.Fatalf("preset video_website_code: %d %s", codeR.Status, codeR.Body)
	}
	cFields := codeR.JSON(t)["fields"].(map[string]any)
	if codeR.JSON(t)["preset"] != "video_website_code" || cFields["video_url"] != "required" || cFields["site"] != "required" || cFields["code_url"] != "required" {
		t.Fatalf("video_website_code preset: %#v", codeR.JSON(t))
	}

	// Back to video_or_website, then a field override demotes the stored preset to custom.
	if r := a.At(t, "PUT", base, map[string]any{"preset": "video_or_website"}, a.Key(w.Org)); r.Status != 200 {
		t.Fatalf("back to video_or_website: %d %s", r.Status, r.Body)
	}
	overR := a.At(t, "PUT", base, map[string]any{"fields": map[string]string{"screenshot": "required"}}, a.Key(w.Org))
	if overR.Status != 200 || overR.JSON(t)["preset"] != "custom" {
		t.Fatalf("field override -> custom: %d %#v", overR.Status, overR.JSON(t))
	}
	oFields := overR.JSON(t)["fields"].(map[string]any)
	if oFields["screenshot"] != "required" || oFields["audience"] != "optional" {
		t.Fatalf("override kept the rest of video_or_website: %#v", oFields)
	}

	// preset + a differing field in the same call also stores custom.
	combo := a.At(t, "PUT", base, map[string]any{"preset": "video_or_website", "fields": map[string]string{"description": "required"}}, a.Key(w.Org))
	if combo.Status != 200 || combo.JSON(t)["preset"] != "custom" {
		t.Fatalf("preset+override -> custom: %d %#v", combo.Status, combo.JSON(t))
	}

	// Set up one submitted and one draft entry for the dry run's impact
	// counts, under the current (combo) rules.
	submitted, draft := w.Teams[0], w.Teams[1]
	full := hacktest.CompleteFields()
	full["description"] = "has a description"
	if r := a.At(t, "PUT", w.EntryPath(), full, a.Key(submitted.Person)); r.Status != 200 {
		t.Fatalf("submitted entry: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", "/v1/hack/events/"+w.Slug+"/entry/submit", map[string]any{}, a.Key(submitted.Person)); r.Status != 200 {
		t.Fatalf("submit: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", w.EntryPath(), full, a.Key(draft.Person)); r.Status != 200 {
		t.Fatalf("draft entry: %d %s", r.Status, r.Body)
	}
	// A draft that is already incomplete is not "newly blocked" by a
	// tighter rule, even though it now misses one more line.
	if r := a.At(t, "PUT", w.EntryPath(), map[string]string{"title": "Only a title"}, a.Key(w.Teams[2].Person)); r.Status != 200 {
		t.Fatalf("incomplete draft: %d %s", r.Status, r.Body)
	}

	// dry_run: tighten "problem" to required... it already is under build;
	// instead drop audience's requirement and turn "code_url" on to see a
	// real newly-blocked count: require code_url, which neither team has set.
	dry := a.At(t, "PUT", base, map[string]any{"dry_run": true, "fields": map[string]string{"code_url": "required"}}, a.Key(w.Org))
	if dry.Status != 200 {
		t.Fatalf("dry run: %d %s", dry.Status, dry.Body)
	}
	impact := dry.JSON(t)["impact"].(map[string]any)
	if impact["submitted_missing"] != float64(1) || impact["drafts_newly_blocked"] != float64(1) {
		t.Fatalf("dry run impact: %#v", impact)
	}
	// dry_run writes nothing: re-GET shows the old rules.
	after := a.At(t, "GET", base, nil, a.Key(w.Org)).JSON(t)
	afterFields := after["fields"].(map[string]any)
	if afterFields["code_url"] == "required" {
		t.Fatalf("dry run persisted a write: %#v", after)
	}

	// The submitted entry stays submitted even after a real tightening.
	if r := a.At(t, "PUT", base, map[string]any{"fields": map[string]string{"code_url": "required"}}, a.Key(w.Org)); r.Status != 200 {
		t.Fatalf("tighten: %d %s", r.Status, r.Body)
	}
	sView := a.At(t, "GET", w.EntryPath(), nil, a.Key(submitted.Person)).JSON(t)
	if sView["status"] != "submitted" {
		t.Fatalf("tightening un-submitted an entry: %#v", sView)
	}

	// 409 once rules are closed (stage closed or later).
	if r := a.SetStage(t, "/v1/hack/events/"+w.Slug, "closed", a.Key(w.Org)); r.Status != 200 {
		t.Fatalf("close: %d %s", r.Status, r.Body)
	}
	hacktest.WantCode(t, "closed non-dry-run", a.At(t, "PUT", base, map[string]any{"preset": "video_website_code"}, a.Key(w.Org)), 409, "entry_rules_closed")
	hacktest.WantCode(t, "closed dry run too", a.At(t, "PUT", base, map[string]any{"dry_run": true, "preset": "video_website_code"}, a.Key(w.Org)), 409, "entry_rules_closed")
}

// --- listEntries counts -----------------------------------------------------

// --- CSV: csvSafe neutralises a formula-looking cell ------------------------
