package hackhttp

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
)

func TestNavigationHelpPresentationEnums(t *testing.T) {
	// hackhttp cannot import a rules package: inspect the JSON source embedded
	// by hackevents.NavigationHelp instead of adding a forbidden dependency.
	raw, err := os.ReadFile("../rules/hackevents/navigation-help.json")
	if err != nil {
		t.Fatal(err)
	}
	var help struct {
		Records []struct {
			ID      string
			Options []struct{ Label, Means string }
		}
	}
	if err := json.Unmarshal(raw, &help); err != nil {
		t.Fatal(err)
	}
	options := map[string]map[string]bool{}
	for _, r := range help.Records {
		options[r.ID] = map[string]bool{}
		for _, o := range r.Options {
			if o.Means != "" {
				options[r.ID][o.Label] = true
			}
		}
	}
	check := func(record string, labels []string) {
		t.Helper()
		for _, label := range labels {
			if !options[record][label] {
				t.Errorf("help record %s: add server option %q with its exact UI label and meaning", record, label)
			}
		}
		if len(options[record]) != len(labels) {
			t.Errorf("help record %s: expected %d server options, got %d", record, len(labels), len(options[record]))
		}
	}
	title := func(value string) string { return strings.ToUpper(value[:1]) + value[1:] }
	styles := []string{"Classic"}
	for _, theme := range events.Themes {
		styles = append(styles, title(theme))
	}
	check("look.style", styles)
	colours := []string{"Automatic"}
	for _, crayon := range events.Crayons {
		colours = append(colours, title(crayon.Name))
	}
	check("look.colour", colours)
	stageLabels := map[string]string{"draft": "Not open yet", "open": "Sign-up open", "building": "Building", "closed": "Entries closed", "judging": "Judging", "results": "Results", "archived": "Ended"}
	stages := []string{}
	for _, stage := range hackStagesOfferedList {
		label, ok := stageLabels[stage]
		if !ok {
			t.Errorf("stage %s: add its UI label to the parity test and the stage help record", stage)
		}
		stages = append(stages, label)
	}
	check("stage", stages)
	modes := append([]string(nil), entries.AssignmentModes...)
	for i := range modes {
		modes[i] = title(modes[i])
	}
	check("judging.mode", modes)
}
