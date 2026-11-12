package hackhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise the same REST routes used by the app and the MCP adapters.
func TestAnnouncementsLinksPinAndPermissions(t *testing.T) {
	a := newHackApp(t)
	org, participant, judge := a.newPerson(t, "ann-org"), a.newPerson(t, "ann-part"), a.newPerson(t, "ann-judge")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, jc := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, participant, "Participant")
	a.joinJudge(t, jc, judge, "Judge")
	base := "/v1/hack/events/" + slug
	path := base + "/announcements"
	post := func(title, body string, pinned bool) string {
		r := a.at(t, "POST", path, map[string]any{"title": title, "body": body, "pinned": pinned}, a.key(org))
		wantTS(t, "post", r, 201, "")
		return r.json(t)["announcement"].(map[string]any)["id"].(string)
	}
	first := post("Old", "https://zoom.us/j/123", true)
	second := post("New", "New body", false)
	list := func(key map[string]string) []any {
		t.Helper()
		r := a.at(t, "GET", path, nil, key)
		wantTS(t, "list", r, 200, "")
		return r.json(t)["announcements"].([]any)
	}
	checkTop := func(id string, pin bool) {
		t.Helper()
		rows := list(a.key(participant))
		row := rows[0].(map[string]any)
		if row["id"] != id || row["pinned"] != pin {
			t.Fatalf("order/pin: %v", rows)
		}
		n := 0
		for _, r := range rows {
			if r.(map[string]any)["pinned"] == true {
				n++
			}
		}
		if n > 1 {
			t.Fatalf("multiple pins: %v", rows)
		}
	}
	checkTop(first, true)
	for _, p := range []person{participant, judge} {
		for _, req := range []struct {
			method, path string
			body         any
		}{
			{"POST", path, map[string]any{"title": "x", "body": "x"}},
			{"PUT", path + "/" + first, map[string]any{"pinned": true}},
			{"PUT", path + "/" + first, map[string]any{"title": "x", "body": "x"}},
			{"DELETE", path + "/" + first, nil},
			{"POST", path + "/preview", map[string]any{"body": "x"}},
		} {
			// Non-organisers get the same "event not found" as every other
			// organiser-only route.
			wantTS(t, "member refusal", a.at(t, req.method, req.path, req.body, a.key(p)), 404, "")
		}
	}
	if len(list(a.key(judge))) != 2 {
		t.Fatal("judges cannot read announcements")
	}
	for _, req := range []struct {
		method, path string
		body         any
	}{
		{"GET", path, nil}, {"POST", path, map[string]any{"title": "x", "body": "x"}}, {"PUT", path + "/" + first, map[string]any{"pinned": true}}, {"DELETE", path + "/" + first, nil}, {"POST", path + "/preview", map[string]any{"body": "x"}},
	} {
		wantTS(t, "unauthenticated", a.at(t, req.method, req.path, req.body, nil), 401, "")
	}
	r := a.at(t, "POST", path+"/preview", map[string]any{"body": "<script> & **bold**\nhttps://zoom.us/j/123"}, a.key(org))
	wantTS(t, "preview", r, 200, "")
	html := r.json(t)["body_html"].(string)
	if strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") || !strings.Contains(html, "ann-card") {
		t.Fatal(html)
	}
	if len(list(a.key(org))) != 2 {
		t.Fatal("preview stored an announcement")
	}
	wantTS(t, "preview too long", a.at(t, "POST", path+"/preview", map[string]any{"body": strings.Repeat("a", 3001)}, a.key(org)), 400, "")
	wantTS(t, "preview empty", a.at(t, "POST", path+"/preview", map[string]any{}, a.key(org)), 200, "")
	wantTS(t, "pin replacement", a.at(t, "PUT", path+"/"+second, map[string]any{"pinned": true}, a.key(org)), 204, "")
	checkTop(second, true)
	row := list(a.key(org))[0].(map[string]any)
	if row["title"] != "New" || row["body"] != "New body" {
		t.Fatalf("pin-only changed text: %v", row)
	}
	wantTS(t, "edit", a.at(t, "PUT", path+"/"+second, map[string]any{"title": "Edited", "body": "Edited body"}, a.key(org)), 204, "")
	checkTop(second, true)
	wantTS(t, "unpin", a.at(t, "PUT", path+"/"+second, map[string]any{"pinned": false}, a.key(org)), 204, "")
	checkTop(second, false)
	third := post("Third", "<script>alert(1)</script>\nhttps://zoom.us/j/123", true)
	checkTop(third, true)
	// Existing unformatted stored text gets links at read time, without migration.
	if _, err := a.database.Exec(`UPDATE event_announcements SET body='Read https://example.com/resources' WHERE id=$1`, first); err != nil {
		t.Fatal(err)
	}
	rows := list(a.key(org))
	old := rows[2].(map[string]any)
	if !strings.Contains(old["body_html"].(string), `href="https://example.com/resources"`) {
		t.Fatalf("old plain text: %v", old)
	}
	pub := a.at(t, "GET", base+"/public", nil, nil)
	wantTS(t, "public", pub, 200, "")
	anns := pub.json(t)["announcements"].([]any)
	if anns[0].(map[string]any)["pinned"] != true || !strings.Contains(anns[0].(map[string]any)["body_html"].(string), "ann-card") {
		t.Fatalf("public: %v", anns)
	}
	wantTS(t, "delete pinned", a.at(t, "DELETE", path+"/"+third, nil, a.key(org)), 204, "")
	checkTop(second, false)
	wantTS(t, "pin", a.at(t, "PUT", path+"/"+first, map[string]any{"pinned": true}, a.key(org)), 204, "")
	if _, err := a.database.Exec(`UPDATE events SET stage='archived' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "archived pin", a.at(t, "PUT", path+"/"+second, map[string]any{"pinned": true}, a.key(org)), 409, "")
}

type announcementCSPPages struct{ navigationBrowserPages }

func (announcementCSPPages) AdminUICSP(next http.Handler) http.Handler {
	return announcementTestCSP(next)
}
func (announcementCSPPages) UICSP(next http.Handler, _ string) http.Handler {
	return announcementTestCSP(next)
}
func announcementTestCSP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; frame-src 'none'")
		next.ServeHTTP(w, r)
	})
}
func TestAnnouncementShellCSP(t *testing.T) {
	mux := http.NewServeMux()
	RegisterHackUI(mux, announcementCSPPages{}, "example.com")
	for _, path := range []string{"/e/sample/team", "/e/sample/manage", "/e/sample/judge", "/events", "/signin"} {
		r := httptest.NewRecorder()
		mux.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		csp := r.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "img-src 'self' data: https://i.ytimg.com;") {
			t.Fatalf("%s: %s", path, csp)
		}
	}
	if !strings.Contains(hackEventPageCSP("nonce", "https://example.com"), "https://i.ytimg.com") {
		t.Fatal("public thumbnail origin missing")
	}
}

func TestAnnouncementMailHTML(t *testing.T) {
	isolateQueueTest(t)
	a := newHackApp(t)
	sink := &contentMailSink{}
	a.hack.SetMailer(sink)
	org := a.newPerson(t, "ann-mail")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	r := a.at(t, "POST", "/v1/hack/events/"+slug+"/announcements", map[string]any{"title": "Kickoff", "body": "[Join kickoff](https://zoom.us/j/123)", "email_participants": true}, a.key(org))
	wantTS(t, "post", r, 201, "")
	a.hack.deliverContentMail(context.Background())
	if len(sink.notices) != 1 || !strings.Contains(sink.notices[0].body, "https://zoom.us/j/123") || !strings.Contains(sink.notices[0].html, "Join the call") || !strings.Contains(sink.notices[0].html, "<table") {
		t.Fatalf("mail: %+v", sink.notices)
	}
}
