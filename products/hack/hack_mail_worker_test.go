package hack

import (
	"context"
	"strings"
	"testing"

	hacktest "github.com/vsriram/simple-host/products/hack/hacktest"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

func TestHackContentAndMail(t *testing.T) {
	testdb.IsolateQueueTest(t)
	a := newHackApp(t)
	sink := &hacktest.MailSink{}
	a.hack.SetMailer(sink)
	org, participant, outsider := a.NewPerson(t, "content-org"), a.NewPerson(t, "content-part"), a.NewPerson(t, "content-out")
	slug, other := hacktest.UniqueSlug(), hacktest.UniqueSlug()
	if r := a.CreateEvent(t, org, slug, nil); r.Status != 201 {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	if r := a.CreateEvent(t, outsider, other, nil); r.Status != 201 {
		t.Fatalf("other: %d %s", r.Status, r.Body)
	}
	base := "/v1/hack/events/" + slug
	content := map[string]any{
		"sponsors": []map[string]string{{"name": "Sponsor & Co", "tier": "Gold", "url": "https://example.com"}},
		"faq":      []map[string]string{{"question": "Who can join?", "answer": "Anyone."}},
		"schedule": []map[string]string{{"title": "Opening", "description": "Welcome", "start_at": "2026-10-02T10:00", "end_at": "2026-10-02T11:00"}},
	}
	if r := a.At(t, "PUT", base+"/content", content, a.Key(org)); r.Status != 200 {
		t.Fatalf("content: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", base+"/content", content, a.Key(outsider)); r.Status != 404 {
		t.Fatalf("cross-event write: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", base+"/content", nil, a.Key(outsider)); r.Status != 404 {
		t.Fatalf("cross-event read: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", base+"/content", map[string]any{"sponsors": []map[string]string{{"name": "Bad", "logo_data": "https://tracker.example/logo.png"}}}, a.Key(org)); r.Status != 400 {
		t.Fatalf("remote logo: %d %s", r.Status, r.Body)
	}
	a.OpenEvent(t, org, slug)
	join := a.At(t, "GET", base, nil, a.Key(org)).JSON(t)["organiser"].(map[string]any)["join_code"].(string)
	if r := a.At(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "Pat"}, a.Key(participant)); r.Status != 200 {
		t.Fatalf("join: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", base+"/content", nil, a.Key(participant)); r.Status != 200 || !strings.Contains(string(r.Body), "Sponsor") {
		t.Fatalf("participant content: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", base+"/content", content, a.Key(participant)); r.Status != 404 {
		t.Fatalf("participant write: %d %s", r.Status, r.Body)
	}
	announcement := map[string]any{"title": "Welcome", "body": "Build something kind.", "email_participants": true}
	r := a.At(t, "POST", base+"/announcements", announcement, a.Key(org))
	// org (the poster) is email-shaped and not a participant here, so the
	// organiser copy is a second, distinct delivery alongside the participant's.
	if r.Status != 201 || r.JSON(t)["emails_queued"] != float64(2) || r.JSON(t)["organiser_copied"] != true {
		t.Fatalf("announcement: %d %s", r.Status, r.Body)
	}
	owner := a.hack.ContentDeliveryOwner().(*contentDeliveryOwner)
	if n, ok := owner.pending(context.Background()); !ok || n != 2 {
		t.Fatalf("queued mail: %d, available=%v", n, ok)
	}
	owner.run(context.Background())
	status := owner.Status()[0]
	if status.LastAttempt == nil || status.LastProgress == nil || status.Pending == nil || *status.Pending != 0 {
		t.Fatalf("mail status: %+v", status)
	}
	if len(sink.Notices) != 2 {
		t.Fatalf("announcement mail: %+v", sink.Notices)
	}
	gotParticipant, gotOrganiser := false, false
	for _, n := range sink.Notices {
		if !strings.Contains(n.Body, "Build something kind.") {
			t.Fatalf("wrong body: %+v", n)
		}
		gotParticipant = gotParticipant || n.To == participant.Email
		gotOrganiser = gotOrganiser || n.To == org.Email
	}
	if !gotParticipant || !gotOrganiser {
		t.Fatalf("missing recipient: %+v", sink.Notices)
	}
	anns := a.At(t, "GET", base+"/announcements", nil, a.Key(org)).JSON(t)["announcements"].([]any)
	if len(anns) == 0 {
		t.Fatalf("no announcements")
	}
	// Posted announcements report emails_sent -- deliveries actually sent,
	// not just the emails_queued the POST response returned.
	if first, _ := anns[0].(map[string]any); first["emails_sent"] != float64(2) {
		t.Fatalf("emails_sent: %v", first)
	}
	if r := a.At(t, "GET", base+"/announcements", nil, a.Key(outsider)); r.Status != 404 {
		t.Fatalf("cross-event announcements: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", base+"/stage", map[string]string{"stage": "archived"}, a.Key(org)); r.Status != 200 {
		t.Fatalf("archive: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", base+"/content", content, a.Key(org)); r.Status != 409 {
		t.Fatalf("archived content: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", base+"/announcements", announcement, a.Key(org)); r.Status != 409 {
		t.Fatalf("archived announcement: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", base+"/content", nil, a.Key(participant)); r.Status != 200 {
		t.Fatalf("archived read: %d %s", r.Status, r.Body)
	}
}
