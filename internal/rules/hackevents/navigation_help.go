package hackevents

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// navigation-help.json is the explanation layer of the shared navigation
// map (navigation.json, SIM-388): one record per organiser setting, option,
// feature and action, each tied to a navigation target for its link. It is
// the organiser assistant's only product knowledge (organiser assistant v2,
// 2026-11-09). Kept as a sibling file so GET /v1/hack/navigation and the
// browser's generated navigation-data.js stay as they are; checked together
// with navigation.json by scripts/build-hack-navigation.py --check and
// TestNavigationHelp. A settings change is not done until its record here
// says what it now does.
//
//go:embed navigation-help.json
var navigationHelpJSON []byte

// HelpOption is one choice of a setting, labelled exactly as the UI shows it.
type HelpOption struct {
	Label string `json:"label"`
	Means string `json:"means"`
}

// HelpRecord explains one setting, feature or action.
type HelpRecord struct {
	ID       string       `json:"id"`
	Target   string       `json:"target"`
	Label    string       `json:"label"`
	Sources  []string     `json:"sources"`
	Controls []string     `json:"controls,omitempty"`
	Explains string       `json:"explains"`
	When     string       `json:"when,omitempty"`
	Options  []HelpOption `json:"options,omitempty"`
	Effects  []string     `json:"effects,omitempty"`
	Limits   []string     `json:"limits,omitempty"`
	Keywords []string     `json:"keywords,omitempty"`
	Related  []string     `json:"related,omitempty"`
}

// NavigationHelpMap is the whole file.
type NavigationHelpMap struct {
	Version int          `json:"version"`
	Records []HelpRecord `json:"records"`
}

// NavigationHelp returns a fresh typed copy of the explanation layer.
func NavigationHelp() NavigationHelpMap {
	var m NavigationHelpMap
	if err := json.Unmarshal(navigationHelpJSON, &m); err != nil {
		panic(err)
	}
	return m
}

// HelpText renders one record as the compact plain text the assistant's
// prompt carries. href is the record's resolved link ("" leaves it off).
func (r HelpRecord) HelpText(href string) string {
	var b strings.Builder
	if href != "" {
		fmt.Fprintf(&b, "### %s [%s](%s)\n", r.Label, r.Label, href)
	} else {
		fmt.Fprintf(&b, "### %s\n", r.Label)
	}
	b.WriteString(r.Explains)
	b.WriteString("\n")
	if r.When != "" {
		b.WriteString("When to use: ")
		b.WriteString(r.When)
		b.WriteString("\n")
	}
	for _, o := range r.Options {
		fmt.Fprintf(&b, "- Option \"%s\": %s\n", o.Label, o.Means)
	}
	for _, e := range r.Effects {
		b.WriteString("- ")
		b.WriteString(e)
		b.WriteString("\n")
	}
	for _, l := range r.Limits {
		b.WriteString("- Limit: ")
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String()
}
