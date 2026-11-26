package hackevents

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// navigation-help.json is the explanation layer of the shared navigation map.
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

// HelpText renders one record as the compact plain text the assistant's prompt carries.
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
