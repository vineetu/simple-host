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
