package hackhttp

import "testing"

func TestHackEntryConflictAndRemovalNotice(t *testing.T) {
	w := newHackEntryWorld(t)
	a := w.a
	base := "/v1/hack/events/" + w.slug
	put := func(body map[string]any) resp { return a.at(t, "PUT", base+"/entry", body, a.key(w.p1)) }
	first := put(map[string]any{"title": "First", "updated_at": ""})
	wantTS(t, "first", first, 200, "")
	stamp := first.json(t)["entry"].(map[string]any)["updated_at"]
	second := put(map[string]any{"title": "Newer", "updated_at": stamp})
	wantTS(t, "second", second, 200, "")
	conflict := put(map[string]any{"title": "Stale", "updated_at": stamp})
	wantTS(t, "stale", conflict, 409, "entry_changed")
	if conflict.json(t)["entry"].(map[string]any)["title"] != "Newer" {
		t.Fatal("missing current text")
	}
	var uid string
	if err := a.database.QueryRow(`SELECT id FROM users WHERE username=$1`, w.p1.email).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "remove", a.at(t, "DELETE", base+"/teams/"+w.alpha+"/members/"+uid, nil, a.key(w.org)), 204, "")
	me := a.at(t, "GET", base, nil, a.key(w.p1)).json(t)["me"].(map[string]any)
	if me["team_removal_notice"] != "The organiser took you off Alpha." {
		t.Fatalf("notice: %v", me)
	}
	wantTS(t, "delete team", a.at(t, "DELETE", base+"/teams/"+w.zeta, nil, a.key(w.org)), 204, "")
	me = a.at(t, "GET", base, nil, a.key(w.p2)).json(t)["me"].(map[string]any)
	if me["team_removal_notice"] != "zeta was removed by the organiser." {
		t.Fatalf("notice: %v", me)
	}
}
