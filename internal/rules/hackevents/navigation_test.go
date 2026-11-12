package hackevents

import (
	"encoding/json"
	"net/url"
	"testing"
)

func TestNavigationMap(t *testing.T) {
	m := Navigation()
	if m.Version != 1 || !json.Valid(NavigationJSON()) {
		t.Fatal("invalid embedded navigation")
	}
	seen := map[string]bool{}
	screens := map[string]bool{}
	for _, s := range m.Screens {
		screens[s.ID] = true
	}
	for _, target := range m.Targets {
		if seen[target.ID] || !screens[target.Screen] {
			t.Fatalf("invalid target: %+v", target)
		}
		seen[target.ID] = true
		if _, err := Resolve(target.ID, "demo", map[string]string{"team": "team & 1", "event_url": "https://demo.example.org/"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range m.Aliases {
		if !seen[a.Target] {
			t.Fatalf("alias target %s missing", a.Target)
		}
	}
	m.Targets[0].ID = "changed"
	if Navigation().Targets[0].ID == "changed" {
		t.Fatal("accessor shared mutable data")
	}
}
func TestResolveNavigation(t *testing.T) {
	link, err := Resolve("j.conflicts", "demo", nil)
	if err != nil || link.Href != "/e/demo/judge?s=queue&tab=conflicts#conflicts" {
		t.Fatalf("%+v %v", link, err)
	}
	link, err = Resolve("j.scoring", "space name", map[string]string{"team": "a&b/+"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link.Href)
	if err != nil || u.Query().Get("team") != "a&b/+" || u.Path != "/e/space name/judge" {
		t.Fatalf("bad escaping: %s %v", link.Href, err)
	}
	if _, err := Resolve("missing", "demo", nil); err == nil {
		t.Fatal("unknown target accepted")
	}
	if _, err := Resolve("j.scoring", "demo", nil); err == nil {
		t.Fatal("missing parameter accepted")
	}
}

func TestValidateNavigation(t *testing.T) {
	m := Navigation()
	if err := ValidateNavigation(m); err != nil {
		t.Fatal(err)
	}
	m.Targets = append(m.Targets, m.Targets[0])
	if err := ValidateNavigation(m); err == nil {
		t.Fatal("duplicate target accepted")
	}
	m = Navigation()
	m.Aliases[0].Target = "missing"
	if err := ValidateNavigation(m); err == nil {
		t.Fatal("missing alias target accepted")
	}
	m = Navigation()
	m.Targets[0].Screen = "missing"
	if err := ValidateNavigation(m); err == nil {
		t.Fatal("missing screen accepted")
	}
}

func TestSubmissionsNavigation(t *testing.T) {
	link, err := Resolve("p.submissions", "demo", nil)
	if err != nil || link.Href != "/e/demo/team?s=submissions" {
		t.Fatalf("%+v %v", link, err)
	}
	for _, role := range Navigation().Roles {
		if role.ID != "participant" {
			continue
		}
		for _, item := range role.Nav {
			if item.ID != "submissions" {
				continue
			}
			if item.Order != 65 || item.PhoneTab || len(item.Stages) != 3 || item.Stages[0] != "judging" || item.Stages[2] != "ended" {
				t.Fatalf("%+v", item)
			}
			return
		}
	}
	t.Fatal("missing participant submissions")
}
