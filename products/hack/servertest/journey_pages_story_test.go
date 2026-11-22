package servertest

import (
	"testing"

	hacktest "github.com/vsriram/simple-host/products/hack/hacktest"
)

// TestHackEventThemeSurfaces is SIM-353: the organiser's Look setting.
func TestHackEventThemeSurfaces(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "theme-org")
	slug := hacktest.UniqueSlug()
	if r := a.CreateEvent(t, org, slug, nil); r.Status != 201 {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	t.Cleanup(func() { a.CleanupEvent(slug) })
	base := "/v1/hack/events/" + slug

	// A fresh event is Classic: no theme set.
	view := a.At(t, "GET", base, nil, a.Key(org)).JSON(t)["event"].(map[string]any)
	if view["theme"] != "" {
		t.Fatalf("new event already has a theme: %v", view["theme"])
	}

	hacktest.WantTS(t, "theme choice", a.At(t, "PATCH", base, map[string]string{"theme": "poster"}, a.Key(org)), 200, "")
	view = a.At(t, "GET", base, nil, a.Key(org)).JSON(t)["event"].(map[string]any)
	if view["theme"] != "poster" {
		t.Fatalf("theme readback %v", view["theme"])
	}

	// Review fix: a PATCH that touches an unrelated field (title) must not
	// reset theme to its zero value -- only a PATCH that actually names
	// "theme" should change it.
	hacktest.WantTS(t, "unrelated patch keeps theme", a.At(t, "PATCH", base, map[string]string{"title": "Theme Keeps Title"}, a.Key(org)), 200, "")
	view = a.At(t, "GET", base, nil, a.Key(org)).JSON(t)["event"].(map[string]any)
	if view["theme"] != "poster" {
		t.Fatalf("theme did not survive an unrelated title patch: %v", view["theme"])
	}

	hacktest.WantTS(t, "invalid theme", a.At(t, "PATCH", base, map[string]string{"theme": "midnight"}, a.Key(org)), 400, "invalid_theme")

	hacktest.WantTS(t, "reset theme", a.At(t, "PATCH", base, map[string]string{"theme": ""}, a.Key(org)), 200, "")
	view = a.At(t, "GET", base, nil, a.Key(org)).JSON(t)["event"].(map[string]any)
	if view["theme"] != "" {
		t.Fatalf("theme did not reset to classic: %v", view["theme"])
	}

	// GET and the public JSON both carry theme.
	hacktest.WantTS(t, "theme set for public checks", a.At(t, "PATCH", base, map[string]string{"theme": "garden"}, a.Key(org)), 200, "")
	view = a.At(t, "GET", base, nil, a.Key(org)).JSON(t)["event"].(map[string]any)
	if view["theme"] != "garden" {
		t.Fatalf("GET event theme %v", view["theme"])
	}
	pub := a.At(t, "GET", base+"/public", nil, nil)
	if pub.Status != 200 || pub.JSON(t)["theme"] != "garden" {
		t.Fatalf("public feed theme: %d %v", pub.Status, pub.JSON(t)["theme"])
	}
}
