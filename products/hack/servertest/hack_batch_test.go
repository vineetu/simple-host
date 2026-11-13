package servertest

import "testing"

func TestHackAnnouncementEditDelete(t *testing.T) {
	a := newTeamSiteApp(t)
	org, other := a.newPerson(t, "ann-owner"), a.newPerson(t, "ann-other")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	created := a.api(t, "POST", base+"/announcements", map[string]any{"title": "Before", "body": "Original"}, org.key)
	wantTS(t, "create", created, 201, "")
	id := created.json(t)["announcement"].(map[string]any)["id"].(string)
	path := base + "/announcements/" + id
	body := map[string]any{"title": "After", "body": "Changed"}
	wantTS(t, "outsider edit", a.api(t, "PUT", path, body, other.key), 404, "event_not_found")
	wantTS(t, "edit", a.api(t, "PUT", path, body, org.key), 204, "")
	items := a.api(t, "GET", base+"/announcements", nil, org.key).json(t)["announcements"].([]any)
	if items[0].(map[string]any)["body"] != "Changed" {
		t.Fatal("edit not stored")
	}
	wantTS(t, "outsider delete", a.api(t, "DELETE", path, nil, other.key), 404, "event_not_found")
	wantTS(t, "delete", a.api(t, "DELETE", path, nil, org.key), 204, "")
	wantTS(t, "missing", a.api(t, "DELETE", path, nil, org.key), 404, "announcement_not_found")
}
