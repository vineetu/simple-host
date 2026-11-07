package hackevents

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

//go:embed navigation.json
var navigationFS embed.FS

type NavigationScreen struct {
	ID      string `json:"id"`
	Role    string `json:"role"`
	Ability string `json:"ability"`
	Path    string `json:"path"`
	Section string `json:"section"`
	Module  string `json:"module"`
}
type NavigationTarget struct {
	ID            string  `json:"id"`
	Kind          string  `json:"kind"`
	Label         string  `json:"label"`
	Role          string  `json:"role"`
	Screen        string  `json:"screen"`
	Section       string  `json:"section"`
	Tab           *string `json:"tab"`
	Anchor        *string `json:"anchor"`
	HrefPattern   string  `json:"href_pattern"`
	Nav           bool    `json:"nav"`
	Icon          string  `json:"icon,omitempty"`
	Short         string  `json:"short,omitempty"`
	PhoneTab      bool    `json:"phone_tab,omitempty"`
	Badge         *string `json:"badge,omitempty"`
	StageRule     string  `json:"stage_rule,omitempty"`
	Order         int     `json:"order"`
	Group         *string `json:"group"`
	ActionLabel   string  `json:"action_label,omitempty"`
	SettingsGroup string  `json:"settings_group,omitempty"`
}
type NavigationAlias struct {
	Section        string `json:"section"`
	Tab            string `json:"tab,omitempty"`
	Anchor         string `json:"anchor,omitempty"`
	Target         string `json:"target"`
	UnlessValidTab bool   `json:"unless_valid_tab,omitempty"`
}
type NavigationItem struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	Icon      string   `json:"icon"`
	Short     string   `json:"short,omitempty"`
	PhoneTab  bool     `json:"phone_tab"`
	Badge     *string  `json:"badge"`
	StageRule string   `json:"stage_rule"`
	Order     int      `json:"order"`
	Group     *string  `json:"group"`
	TargetID  string   `json:"target_id,omitempty"`
	Stages    []string `json:"stages,omitempty"`
}
type NavigationRole struct {
	ID  string           `json:"id"`
	Who string           `json:"who"`
	Nav []NavigationItem `json:"nav"`
}
type NavigationSettingsTab struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type NavigationResolution struct {
	DefaultSections         map[string]string       `json:"default_sections"`
	DefaultTabs             map[string]string       `json:"default_tabs"`
	PreserveOtherParameters bool                    `json:"preserve_other_parameters"`
	ReplaceHistory          bool                    `json:"replace_history"`
	Precedence              string                  `json:"precedence"`
	SettingsTabs            []NavigationSettingsTab `json:"settings_tabs"`
}
type NavigationMap struct {
	Version    int                  `json:"version"`
	Screens    []NavigationScreen   `json:"screens"`
	Targets    []NavigationTarget   `json:"targets"`
	Roles      []NavigationRole     `json:"roles"`
	Aliases    []NavigationAlias    `json:"aliases"`
	Resolution NavigationResolution `json:"resolution"`
}
type NavigationLink struct {
	Screen  string  `json:"screen"`
	Section string  `json:"section"`
	Tab     *string `json:"tab"`
	Anchor  *string `json:"anchor"`
	Href    string  `json:"href"`
}

// Navigation returns a fresh typed copy of the shared destination map.
func Navigation() NavigationMap {
	data, _ := navigationFS.ReadFile("navigation.json")
	var m NavigationMap
	if err := json.Unmarshal(data, &m); err != nil {
		panic(err)
	}
	return m
}

// NavigationJSON retains all presentation metadata in the public data contract.
func NavigationJSON() json.RawMessage {
	data, _ := navigationFS.ReadFile("navigation.json")
	return data
}

// Resolve expands a stable destination, escaping path and query parameters separately.
func Resolve(targetID, slug string, params map[string]string) (NavigationLink, error) {
	for _, t := range Navigation().Targets {
		if t.ID != targetID {
			continue
		}
		href := strings.ReplaceAll(t.HrefPattern, "{slug}", url.PathEscape(slug))
		if t.HrefPattern == "{event_url}" {
			if value := params["event_url"]; value != "" {
				href = value
			}
		}
		for k, v := range params {
			href = strings.ReplaceAll(href, "{"+k+"}", url.QueryEscape(v))
		}
		if strings.Contains(href, "{") {
			return NavigationLink{}, fmt.Errorf("missing navigation parameter for %s", targetID)
		}
		return NavigationLink{t.Screen, t.Section, t.Tab, t.Anchor, href}, nil
	}
	return NavigationLink{}, fmt.Errorf("unknown navigation target %q", targetID)
}

// ValidateNavigation checks referential integrity without transport or filesystem dependencies.
func ValidateNavigation(m NavigationMap) error {
	screens := map[string]bool{}
	for _, screen := range m.Screens {
		if screens[screen.ID] {
			return fmt.Errorf("duplicate navigation screen %s", screen.ID)
		}
		screens[screen.ID] = true
	}
	targets := map[string]bool{}
	for _, target := range m.Targets {
		if targets[target.ID] || !screens[target.Screen] {
			return fmt.Errorf("invalid navigation target %s", target.ID)
		}
		targets[target.ID] = true
	}
	for _, alias := range m.Aliases {
		if !targets[alias.Target] {
			return fmt.Errorf("unknown alias target %s", alias.Target)
		}
	}
	for _, role := range m.Roles {
		for _, item := range role.Nav {
			if item.TargetID != "" && !targets[item.TargetID] {
				return fmt.Errorf("unknown nav target %s", item.TargetID)
			}
		}
	}
	return nil
}

// ChecklistTargets resolves checklist destinations through the shared navigation map.
type ChecklistTargets struct{}

func (ChecklistTargets) Resolve(targetID, slug string, params map[string]string) (ChecklistLink, bool) {
	link, err := Resolve(targetID, slug, params)
	return ChecklistLink{Screen: link.Screen, Section: link.Section, Tab: link.Tab, Href: link.Href}, err == nil
}
