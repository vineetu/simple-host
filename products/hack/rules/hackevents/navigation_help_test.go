package hackevents

import (
	"regexp"
	"strings"
	"testing"
)

// Stable organiser links all contain the shared event address {slug}.
// Detail placeholders, such as {team}, need a selected object and are forbidden.
func stableHelpTarget(target NavigationTarget) bool {
	return target.Role == "organiser" && !strings.Contains(strings.ReplaceAll(target.HrefPattern, "{slug}", ""), "{")
}

func TestNavigationHelp(t *testing.T) {
	help, navigation := NavigationHelp(), Navigation()
	if help.Version != 1 || len(help.Records) == 0 {
		t.Fatal("navigation help: expected version 1 and non-empty records")
	}
	targets := map[string]NavigationTarget{}
	for _, target := range navigation.Targets {
		targets[target.ID] = target
	}
	ids, used, sources := map[string]bool{}, map[string]bool{}, map[string]bool{}
	rendered, linked := 0, 0
	for _, record := range help.Records {
		if strings.TrimSpace(record.ID) == "" || ids[record.ID] {
			t.Errorf("help record %q: add a unique non-empty id", record.ID)
		}
		ids[record.ID] = true
		if strings.TrimSpace(record.Label) == "" || strings.TrimSpace(record.Explains) == "" || len(record.Sources) == 0 {
			t.Errorf("help record %s: label, explains and sources are required", record.ID)
		}
		target, ok := targets[record.Target]
		if !ok || !stableHelpTarget(target) {
			t.Errorf("help record %s: choose an existing organiser target without detail placeholders: %s", record.ID, record.Target)
		}
		if (record.Options != nil || !used[record.Target]) && strings.TrimSpace(record.When) == "" {
			t.Errorf("help record %s: options and first record for a target require non-empty when", record.ID)
		}
		used[record.Target] = true
		for _, source := range record.Sources {
			sources[source] = true
		}
		for _, option := range record.Options {
			if strings.TrimSpace(option.Label) == "" || strings.TrimSpace(option.Means) == "" {
				t.Errorf("help record %s: add label and means for every option", record.ID)
			}
		}
		rendered += len(record.HelpText(""))
		link, err := Resolve(record.Target, "demo-hack", nil)
		if err != nil {
			t.Errorf("help record %s: target cannot resolve without detail parameters: %v", record.ID, err)
		}
		linked += len(record.HelpText(link.Href))
	}
	for _, record := range help.Records {
		for _, related := range record.Related {
			if !ids[related] {
				t.Errorf("help record %s: related %q needs a record", record.ID, related)
			}
		}
	}
	for _, target := range navigation.Targets {
		if stableHelpTarget(target) && (target.Kind == "setting" || target.Kind == "section" || target.Kind == "action") && !used[target.ID] {
			t.Errorf("organiser target %s: add a help record targeting it", target.ID)
		}
	}
	for _, screen := range navigation.Screens {
		if screen.Role == "organiser" && !sources["screens/"+screen.Module+".js"] {
			t.Errorf("organiser screen %s: add an explanation citing its module", screen.ID)
		}
	}
	// The whole layer goes into every assistant prompt: keep it compact
	// (the whole-prompt guard is in internal/hackhttp's assistant tests).
	if rendered >= 56_000 {
		t.Errorf("rendered help is %d bytes; tighten wording to < 56 KB, or select records per question", rendered)
	}
	if linked >= 56_000 {
		t.Errorf("help with demo-hack links is %d bytes; tighten wording to < 56 KB, or select records per question", linked)
	}
	t.Logf("%d records; sum(len(HelpText(\"\")))=%d bytes; with demo-hack links=%d bytes", len(help.Records), rendered, linked)
	help.Records[0].Label = "changed"
	if NavigationHelp().Records[0].Label == "changed" {
		t.Fatal("NavigationHelp shares mutable data")
	}
}

// TestNavigationHelpPlainWords: the explanations are read out to organisers,
// so no internal field names (snake_case) or route paths.
func TestNavigationHelpPlainWords(t *testing.T) {
	internal := regexp.MustCompile(`\b[a-z]+_[a-z0-9_]+\b|/v1/`)
	for _, r := range NavigationHelp().Records {
		texts := append([]string{r.Explains, r.When}, r.Effects...)
		texts = append(texts, r.Limits...)
		for _, o := range r.Options {
			texts = append(texts, o.Means)
		}
		for _, s := range texts {
			if m := internal.FindString(s); m != "" {
				t.Errorf("record %s: %q is an internal name; say it in the organiser's words", r.ID, m)
			}
		}
	}
}
