package hackentries

import (
	"encoding/json"
	"os"
	"testing"
)

// Read the same embedded source without a sibling rules-package dependency:
// rules packages may depend only on model (scripts/layering/rules.txt).
func TestNavigationHelpEntryEnums(t *testing.T) {
	raw, err := os.ReadFile("../hackevents/navigation-help.json")
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
	presets := map[string]string{"video_or_website": "Video or website", "video_website_code": "Video, website and code link"}
	selectable := 0
	for preset := range Presets {
		if !PresetSettable(preset) {
			continue
		}
		selectable++
		label, ok := presets[preset]
		if !ok || !options["entry.rules"][label] {
			t.Errorf("selectable entry preset %s: add its exact UI option and meaning to entry.rules", preset)
		}
	}
	if selectable != len(options["entry.rules"]) {
		t.Errorf("entry.rules: explain exactly the %d selectable presets; legacy presets must remain read-only", selectable)
	}
	for _, preset := range PresetOrder {
		if !PresetSettable(preset) {
			t.Errorf("PresetOrder includes unselectable %s", preset)
		}
	}
	modes := map[string]string{"required": "Required", "optional": "Optional", "off": "Off", "one_of": "One of"}
	for mode := range RuleModeOK {
		if !options["entry.evidence"][modes[mode]] {
			t.Errorf("field mode %s: add its exact UI label and meaning to entry.evidence", mode)
		}
	}
	if len(options["entry.evidence"]) != len(RuleModeOK) {
		t.Error("entry.evidence: options drifted from server field modes")
	}
}
