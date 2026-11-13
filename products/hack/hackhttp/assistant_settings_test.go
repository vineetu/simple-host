package hackhttp

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	entries "github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
)

// TestAssistantSettingsLabelsInHelp: every UI label the CURRENT SETTINGS
// block uses (assistant_settings.go) is a label or an option label in the
// navigation map's explanation layer, so the block and KNOWLEDGE call each
// setting and choice by the same name the organiser sees, and renaming a
// UI label (which the explanation-layer check catches) can't leave this
// block on the old name unnoticed.
func TestAssistantSettingsLabelsInHelp(t *testing.T) {
	known := map[string]bool{}
	for _, r := range events.NavigationHelp().Records {
		known[r.Label] = true
		for _, o := range r.Options {
			known[o.Label] = true
		}
	}
	var labels []string
	for _, f := range assistantEntryFieldLabels {
		labels = append(labels, f.Label)
	}
	for _, m := range []map[string]string{assistantFieldModeLabels, assistantPresetLabels, assistantJudgeFirstLabels, assistantAssignmentModeLabels, assistantScoreModeLabels, assistantEligibilityLabels, assistantWebsiteModeLabels} {
		for _, l := range m {
			labels = append(labels, l)
		}
	}
	labels = append(labels, "Full ranking", "Winners only")
	for _, l := range labels {
		if !known[l] {
			t.Errorf("CURRENT SETTINGS label %q is not a label or option label in navigation-help.json", l)
		}
	}
	// Every stored field mode and every entry field has a label.
	for _, mode := range []string{"required", "optional", "off", "one_of"} {
		if assistantFieldModeLabels[mode] == "" {
			t.Errorf("field mode %q has no label", mode)
		}
	}
}

// TestAssistantSettingsCoverEveryEntryField: a new hand-in field can't be
// silently missing from CURRENT SETTINGS.
func TestAssistantSettingsCoverEveryEntryField(t *testing.T) {
	have := map[string]bool{"description": true} // its own line, only when Required
	for _, f := range assistantEntryFieldLabels {
		have[f.ID] = true
	}
	for _, f := range entries.RuleFields {
		if !have[f] {
			t.Errorf("entry field %q has no CURRENT SETTINGS label (assistant_settings.go assistantEntryFieldLabels)", f)
		}
	}
	for _, p := range []string{"video_or_website", "video_website_code", "build", "code", "ideas"} {
		if assistantPresetLabels[p] == "" && assistantLegacyPresetLabels[p] == "" {
			t.Errorf("preset %q has no CURRENT SETTINGS label", p)
		}
	}
}

// TestAssistantSettingsLineLabelsInUI: the setting names CURRENT SETTINGS
// lines use are the organiser screens' own words, so renaming one in the UI
// fails here until assistant_settings.go follows.
func TestAssistantSettingsLineLabelsInUI(t *testing.T) {
	files, err := filepath.Glob("../../../internal/webassets/static/hack/screens/*.js")
	if err != nil || len(files) == 0 {
		t.Fatalf("read screens: %v", err)
	}
	var all strings.Builder
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		all.Write(b)
	}
	ui := strings.NewReplacer("&amp;", "&", "’", "'").Replace(all.String())
	for _, label := range []string{
		"Approve people who join with your link", "Let people apply without your join link",
		"List this event on Find a hackathon", "Team finding", "Most people per team",
		"Entries close", "What teams hand in", "Summary card", "Show your work", "Judges see",
		"Reminder times", "Judging closes", "Rubric", "First tie criterion", "How judges are assigned",
		"Judges per team", "Score used for ranking", "Visibility", "Show scores", "Show ranks",
		"People's choice", "Let visitors vote", "Who can vote", "Show entries on the event page",
		"What shows at your address",
	} {
		if !strings.Contains(ui, label) {
			t.Errorf("CURRENT SETTINGS uses %q, which no organiser screen shows any more: update assistant_settings.go", label)
		}
	}
}

// Behaviour copy must stay in Ask's knowledge as well as on the screen.
func TestAssistantSettingsBehaviourLinesInHelp(t *testing.T) {
	lines := []string{
		"Shows on the team's Overview. Teammates with email updates on also get an email.",
		"Teammates with email updates on also get an email.",
		"Editing it later never sends another email.",
		"Saving changes doesn't email anyone again.",
		"It disappears from your page and from everyone's app. Emails already sent stay sent.",
		"No participant has an email address yet.",
		"Posted. Nobody had an email address on file, so no email was sent.",
		// The count and plural are interpolated; guard the literal prefix.
		"Posted and emailed to",
		"(including a copy to you)",
	}
	normalise := strings.NewReplacer("&amp;", "&", "’", "'").Replace
	var help, ui strings.Builder
	for _, r := range events.NavigationHelp().Records {
		help.WriteString(normalise(strings.Join(append(append([]string{r.Explains, r.When}, r.Effects...), r.Limits...), "\n")))
	}
	files, err := filepath.Glob("../../../internal/webassets/static/hack/screens/*.js")
	if err != nil || len(files) == 0 {
		t.Fatalf("read screens: %v", err)
	}
	// Comments are not screen text. Keep URLs inside strings intact.
	comments := regexp.MustCompile(`(?m)^[\t ]*//[^\n]*|(?s:/\*.*?\*/)`)
	behaviour := regexp.MustCompile("(?i)[^\"'`<>\n]*(?:(?:also )?gets? an email|emailed to|no email was sent|email updates on)[^\"'`<>\n]*")
	for _, f := range files {
		name := filepath.Base(f)
		if !strings.HasPrefix(name, "organiser-") && !strings.HasPrefix(name, "_") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		text := normalise(comments.ReplaceAllString(string(b), ""))
		ui.WriteString(text)
		for _, raw := range behaviour.FindAllString(text, -1) {
			fragment, rest, interpolated := strings.Cut(raw, "${")
			fragment = strings.TrimSpace(fragment)
			if interpolated && len(fragment) < 8 {
				_, fragment, _ = strings.Cut(rest, "}")
				fragment = strings.TrimSpace(fragment)
			}
			found := false
			for _, line := range lines {
				found = found || (fragment != "" && strings.Contains(line, fragment))
			}
			if !found {
				t.Errorf("%s: email behaviour %q needs a listed line and knowledge record", name, raw)
			}
		}
	}
	for _, line := range lines {
		if !strings.Contains(ui.String(), line) {
			t.Errorf("UI behaviour line missing: %q", line)
		}
		if !strings.Contains(help.String(), line) {
			t.Errorf("knowledge behaviour line missing: %q", line)
		}
	}
}
