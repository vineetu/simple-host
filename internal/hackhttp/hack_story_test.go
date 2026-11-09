package hackhttp

import (
	"strings"
	"testing"
)

func TestHackEventColourSurfaces(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "ink-org")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	t.Cleanup(func() { a.cleanupEvent(slug) })
	base := "/v1/hack/events/" + slug
	wantTS(t, "colour choice", a.at(t, "PATCH", base, map[string]string{"accent_color": "yellow"}, a.key(org)), 200, "")
	view := a.at(t, "GET", base, nil, a.key(org)).json(t)["event"].(map[string]any)
	if view["accent_color"] != "yellow" || view["color"] != "#f2c94c" {
		t.Fatalf("colour readback %v", view["color"])
	}
	icon := a.at(t, "GET", base+"/icon", nil, nil)
	if !strings.Contains(string(icon.body), `fill="#f2c94c"`) {
		t.Fatal("fallback icon differs from event colour")
	}
	wantTS(t, "invalid colour", a.at(t, "PATCH", base, map[string]string{"accent_color": "url(evil)"}, a.key(org)), 400, "invalid_accent_color")
	wantTS(t, "reset colour", a.at(t, "PATCH", base, map[string]string{"accent_color": ""}, a.key(org)), 200, "")
	view = a.at(t, "GET", base, nil, a.key(org)).json(t)["event"].(map[string]any)
	if view["color"] != hackEventColor(slug, "") {
		t.Fatal("automatic colour not stable")
	}
}

// TestHackEventThemeSurfaces is SIM-353: the organiser's Look setting.
func TestHackEventThemeSurfaces(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "theme-org")
	participant := a.newPerson(t, "theme-mem")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	t.Cleanup(func() { a.cleanupEvent(slug) })
	base := "/v1/hack/events/" + slug

	// A fresh event is Classic: no theme set.
	view := a.at(t, "GET", base, nil, a.key(org)).json(t)["event"].(map[string]any)
	if view["theme"] != "" {
		t.Fatalf("new event already has a theme: %v", view["theme"])
	}

	wantTS(t, "theme choice", a.at(t, "PATCH", base, map[string]string{"theme": "poster"}, a.key(org)), 200, "")
	view = a.at(t, "GET", base, nil, a.key(org)).json(t)["event"].(map[string]any)
	if view["theme"] != "poster" {
		t.Fatalf("theme readback %v", view["theme"])
	}

	wantTS(t, "invalid theme", a.at(t, "PATCH", base, map[string]string{"theme": "midnight"}, a.key(org)), 400, "invalid_theme")

	wantTS(t, "reset theme", a.at(t, "PATCH", base, map[string]string{"theme": ""}, a.key(org)), 200, "")
	view = a.at(t, "GET", base, nil, a.key(org)).json(t)["event"].(map[string]any)
	if view["theme"] != "" {
		t.Fatalf("theme did not reset to classic: %v", view["theme"])
	}

	// Non-organiser: refused the same way any other PATCH is (404, hiding
	// whether the event exists, same as TestHackApplyPatchEventToggle).
	a.openEvent(t, org, slug)
	code := a.at(t, "GET", base, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	wantTS(t, "participant joins", a.at(t, "POST", "/v1/hack/join/"+code, map[string]any{"accept_coc": true, "display_name": "P"}, a.key(participant)), 200, "")
	wantTS(t, "non-organiser refused", a.at(t, "PATCH", base, map[string]string{"theme": "poster"}, a.key(participant)), 404, "event_not_found")

	// GET and the public JSON both carry theme.
	wantTS(t, "theme set for public checks", a.at(t, "PATCH", base, map[string]string{"theme": "garden"}, a.key(org)), 200, "")
	view = a.at(t, "GET", base, nil, a.key(org)).json(t)["event"].(map[string]any)
	if view["theme"] != "garden" {
		t.Fatalf("GET event theme %v", view["theme"])
	}
	pub := a.at(t, "GET", base+"/public", nil, nil)
	if pub.status != 200 || pub.json(t)["theme"] != "garden" {
		t.Fatalf("public feed theme: %d %v", pub.status, pub.json(t)["theme"])
	}
}
