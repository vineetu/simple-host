package servertest

import (
	"context"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/products/hack/hacktest"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

// Exercise the same REST routes used by the app and the MCP adapters.
func TestAnnouncementsLinksPinAndPermissions(t *testing.T) {
	a := newFeatApp(t)
	org, participant, judge := a.NewPerson(t, "ann-org"), a.NewPerson(t, "ann-part"), a.NewPerson(t, "ann-judge")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	a.OpenEvent(t, org, slug)
	join, jc := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, participant, "Participant")
	a.JoinJudge(t, jc, judge, "Judge")
	base := "/v1/hack/events/" + slug
	path := base + "/announcements"
	post := func(title, body string, pinned bool) string {
		r := a.At(t, "POST", path, map[string]any{"title": title, "body": body, "pinned": pinned}, a.Key(org))
		hacktest.WantTS(t, "post", r, 201, "")
		return r.JSON(t)["announcement"].(map[string]any)["id"].(string)
	}
	first := post("Old", "https://zoom.us/j/123", true)
	second := post("New", "New body", false)
	list := func(key map[string]string) []any {
		t.Helper()
		r := a.At(t, "GET", path, nil, key)
		hacktest.WantTS(t, "list", r, 200, "")
		return r.JSON(t)["announcements"].([]any)
	}
	checkTop := func(id string, pin bool) {
		t.Helper()
		rows := list(a.Key(participant))
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
	for _, p := range []hacktest.Person{participant, judge} {
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
			hacktest.WantTS(t, "member refusal", a.At(t, req.method, req.path, req.body, a.Key(p)), 404, "")
		}
	}
	if len(list(a.Key(judge))) != 2 {
		t.Fatal("judges cannot read announcements")
	}
	for _, req := range []struct {
		method, path string
		body         any
	}{
		{"GET", path, nil}, {"POST", path, map[string]any{"title": "x", "body": "x"}}, {"PUT", path + "/" + first, map[string]any{"pinned": true}}, {"DELETE", path + "/" + first, nil}, {"POST", path + "/preview", map[string]any{"body": "x"}},
	} {
		hacktest.WantTS(t, "unauthenticated", a.At(t, req.method, req.path, req.body, nil), 401, "")
	}
	r := a.At(t, "POST", path+"/preview", map[string]any{"body": "<script> & **bold**\nhttps://zoom.us/j/123"}, a.Key(org))
	hacktest.WantTS(t, "preview", r, 200, "")
	html := r.JSON(t)["body_html"].(string)
	if strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") || !strings.Contains(html, "ann-card") {
		t.Fatal(html)
	}
	if len(list(a.Key(org))) != 2 {
		t.Fatal("preview stored an announcement")
	}
	hacktest.WantTS(t, "preview too long", a.At(t, "POST", path+"/preview", map[string]any{"body": strings.Repeat("a", 3001)}, a.Key(org)), 400, "")
	hacktest.WantTS(t, "preview empty", a.At(t, "POST", path+"/preview", map[string]any{}, a.Key(org)), 200, "")
	hacktest.WantTS(t, "pin replacement", a.At(t, "PUT", path+"/"+second, map[string]any{"pinned": true}, a.Key(org)), 204, "")
	checkTop(second, true)
	row := list(a.Key(org))[0].(map[string]any)
	if row["title"] != "New" || row["body"] != "New body" {
		t.Fatalf("pin-only changed text: %v", row)
	}
	hacktest.WantTS(t, "edit", a.At(t, "PUT", path+"/"+second, map[string]any{"title": "Edited", "body": "Edited body"}, a.Key(org)), 204, "")
	checkTop(second, true)
	hacktest.WantTS(t, "unpin", a.At(t, "PUT", path+"/"+second, map[string]any{"pinned": false}, a.Key(org)), 204, "")
	checkTop(second, false)
	third := post("Third", "<script>alert(1)</script>\nhttps://zoom.us/j/123", true)
	checkTop(third, true)
	// Existing unformatted stored text gets links at read time, without migration.
	if _, err := a.DB.Exec(`UPDATE event_announcements SET body='Read https://example.com/resources' WHERE id=$1`, first); err != nil {
		t.Fatal(err)
	}
	rows := list(a.Key(org))
	old := rows[2].(map[string]any)
	if !strings.Contains(old["body_html"].(string), `href="https://example.com/resources"`) {
		t.Fatalf("old plain text: %v", old)
	}
	pub := a.At(t, "GET", base+"/public", nil, nil)
	hacktest.WantTS(t, "public", pub, 200, "")
	anns := pub.JSON(t)["announcements"].([]any)
	if anns[0].(map[string]any)["pinned"] != true || !strings.Contains(anns[0].(map[string]any)["body_html"].(string), "ann-card") {
		t.Fatalf("public: %v", anns)
	}
	hacktest.WantTS(t, "delete pinned", a.At(t, "DELETE", path+"/"+third, nil, a.Key(org)), 204, "")
	checkTop(second, false)
	hacktest.WantTS(t, "pin", a.At(t, "PUT", path+"/"+first, map[string]any{"pinned": true}, a.Key(org)), 204, "")
	if _, err := a.DB.Exec(`UPDATE events SET stage='archived' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	hacktest.WantTS(t, "archived pin", a.At(t, "PUT", path+"/"+second, map[string]any{"pinned": true}, a.Key(org)), 409, "")
}

func TestHackAnnouncementEmailsApprovedApplicantsOnly(t *testing.T) {
	testdb.IsolateQueueTest(t)
	a := newFeatApp(t)
	sink := &hacktest.MailSink{}
	a.hack.SetMailer(sink)
	org, approved, pending, rejected := a.NewPerson(t, "approval-mail-org"), a.NewPerson(t, "approval-mail-approved"), a.NewPerson(t, "approval-mail-pending"), a.NewPerson(t, "approval-mail-rejected")
	slug := hacktest.UniqueSlug()
	if r := a.CreateEvent(t, org, slug, nil); r.Status != 201 {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	a.OpenEvent(t, org, slug)
	base := "/v1/hack/events/" + slug
	if r := a.At(t, "PUT", base+"/registration", map[string]any{"approval_required": true}, a.Key(org)); r.Status != 200 {
		t.Fatalf("settings: %d %s", r.Status, r.Body)
	}
	join := a.At(t, "GET", base, nil, a.Key(org)).JSON(t)["organiser"].(map[string]any)["join_code"].(string)
	for _, p := range []hacktest.Person{approved, pending, rejected} {
		if r := a.At(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "Applicant"}, a.Key(p)); r.Status != 200 {
			t.Fatalf("join: %d %s", r.Status, r.Body)
		}
	}
	for _, tc := range []struct {
		p      hacktest.Person
		status string
	}{{approved, "approved"}, {rejected, "rejected"}} {
		if r := a.At(t, "POST", base+"/applications/"+a.UserID(t, tc.p)+"/decision", map[string]string{"decision": tc.status}, a.Key(org)); r.Status != 200 {
			t.Fatalf("decision: %d %s", r.Status, r.Body)
		}
	}
	r := a.At(t, "POST", base+"/announcements", map[string]any{"title": "Update", "body": "Approved team information", "email_participants": true}, a.Key(org))
	// org is email-shaped and never applied as a participant, so the organiser
	// copy adds a second delivery alongside the one approved applicant's.
	if r.Status != 201 || r.JSON(t)["emails_queued"] != float64(2) || r.JSON(t)["organiser_copied"] != true {
		t.Fatalf("mail queue: %d %s", r.Status, r.Body)
	}
	a.hack.DeliverContentMail(context.Background(), a.hack.Mailer)
	if len(sink.Notices) != 2 {
		t.Fatalf("recipients: %+v", sink.Notices)
	}
	gotApproved, gotOrganiser := false, false
	for _, n := range sink.Notices {
		gotApproved = gotApproved || n.To == approved.Email
		gotOrganiser = gotOrganiser || n.To == org.Email
	}
	if !gotApproved || !gotOrganiser {
		t.Fatalf("recipients: %+v", sink.Notices)
	}
}
