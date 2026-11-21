package hackhttp

import (
	"context"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/email"
)

type contentMailSink struct {
	notices []struct{ to, subject, body, html string }
}

func (*contentMailSink) SendSignInCode(string, string, string) error { return nil }
func (s *contentMailSink) SendNotice(to, subject, body string) error {
	s.notices = append(s.notices, struct{ to, subject, body, html string }{to, subject, body, ""})
	return nil
}

func TestHackContentAndMail(t *testing.T) {
	isolateQueueTest(t)
	a := newHackApp(t)
	sink := &contentMailSink{}
	a.hack.SetMailer(sink)
	org, participant, outsider := a.newPerson(t, "content-org"), a.newPerson(t, "content-part"), a.newPerson(t, "content-out")
	slug, other := uniqueSlug(), uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	if r := a.createEvent(t, outsider, other, nil); r.status != 201 {
		t.Fatalf("other: %d %s", r.status, r.body)
	}
	base := "/v1/hack/events/" + slug
	content := map[string]any{
		"sponsors": []map[string]string{{"name": "Sponsor & Co", "tier": "Gold", "url": "https://example.com"}},
		"faq":      []map[string]string{{"question": "Who can join?", "answer": "Anyone."}},
		"schedule": []map[string]string{{"title": "Opening", "description": "Welcome", "start_at": "2026-10-02T10:00", "end_at": "2026-10-02T11:00"}},
	}
	if r := a.at(t, "PUT", base+"/content", content, a.key(org)); r.status != 200 {
		t.Fatalf("content: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", base+"/content", content, a.key(outsider)); r.status != 404 {
		t.Fatalf("cross-event write: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", base+"/content", nil, a.key(outsider)); r.status != 404 {
		t.Fatalf("cross-event read: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", base+"/content", map[string]any{"sponsors": []map[string]string{{"name": "Bad", "logo_data": "https://tracker.example/logo.png"}}}, a.key(org)); r.status != 400 {
		t.Fatalf("remote logo: %d %s", r.status, r.body)
	}
	a.openEvent(t, org, slug)
	join := a.at(t, "GET", base, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	if r := a.at(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "Pat"}, a.key(participant)); r.status != 200 {
		t.Fatalf("join: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", base+"/content", nil, a.key(participant)); r.status != 200 || !strings.Contains(string(r.body), "Sponsor") {
		t.Fatalf("participant content: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", base+"/content", content, a.key(participant)); r.status != 404 {
		t.Fatalf("participant write: %d %s", r.status, r.body)
	}
	announcement := map[string]any{"title": "Welcome", "body": "Build something kind.", "email_participants": true}
	r := a.at(t, "POST", base+"/announcements", announcement, a.key(org))
	// org (the poster) is email-shaped and not a participant here, so the
	// organiser copy is a second, distinct delivery alongside the participant's.
	if r.status != 201 || r.json(t)["emails_queued"] != float64(2) || r.json(t)["organiser_copied"] != true {
		t.Fatalf("announcement: %d %s", r.status, r.body)
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
	if len(sink.notices) != 2 {
		t.Fatalf("announcement mail: %+v", sink.notices)
	}
	gotParticipant, gotOrganiser := false, false
	for _, n := range sink.notices {
		if !strings.Contains(n.body, "Build something kind.") {
			t.Fatalf("wrong body: %+v", n)
		}
		gotParticipant = gotParticipant || n.to == participant.email
		gotOrganiser = gotOrganiser || n.to == org.email
	}
	if !gotParticipant || !gotOrganiser {
		t.Fatalf("missing recipient: %+v", sink.notices)
	}
	anns := a.at(t, "GET", base+"/announcements", nil, a.key(org)).json(t)["announcements"].([]any)
	if len(anns) == 0 {
		t.Fatalf("no announcements")
	}
	// Posted announcements report emails_sent -- deliveries actually sent,
	// not just the emails_queued the POST response returned.
	if first, _ := anns[0].(map[string]any); first["emails_sent"] != float64(2) {
		t.Fatalf("emails_sent: %v", first)
	}
	if r := a.at(t, "GET", base+"/announcements", nil, a.key(outsider)); r.status != 404 {
		t.Fatalf("cross-event announcements: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", base+"/stage", map[string]string{"stage": "archived"}, a.key(org)); r.status != 200 {
		t.Fatalf("archive: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", base+"/content", content, a.key(org)); r.status != 409 {
		t.Fatalf("archived content: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", base+"/announcements", announcement, a.key(org)); r.status != 409 {
		t.Fatalf("archived announcement: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", base+"/content", nil, a.key(participant)); r.status != 200 {
		t.Fatalf("archived read: %d %s", r.status, r.body)
	}
}

func TestHackAnnouncementRequiresConfiguredEmail(t *testing.T) {
	isolateQueueTest(t)
	a := newHackApp(t)
	a.hack.SetMailer(email.NewResendSender("", "test@example.com"))
	org := a.newPerson(t, "content-no-mail")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	r := a.at(t, "POST", "/v1/hack/events/"+slug+"/announcements", map[string]any{"title": "Hello", "body": "World", "email_participants": true}, a.key(org))
	if r.status != 503 || r.json(t)["code"] != "email_unavailable" {
		t.Fatalf("email unavailable: %d %s", r.status, r.body)
	}
	r = a.at(t, "POST", "/v1/hack/events/"+slug+"/announcements", map[string]any{"title": "Hello", "body": "World"}, a.key(org))
	if r.status != 201 {
		t.Fatalf("web-only announcement: %d %s", r.status, r.body)
	}
}

func TestHackAnnouncementEmailsApprovedApplicantsOnly(t *testing.T) {
	isolateQueueTest(t)
	a := newHackApp(t)
	sink := &contentMailSink{}
	a.hack.SetMailer(sink)
	org, approved, pending, rejected := a.newPerson(t, "approval-mail-org"), a.newPerson(t, "approval-mail-approved"), a.newPerson(t, "approval-mail-pending"), a.newPerson(t, "approval-mail-rejected")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	a.openEvent(t, org, slug)
	base := "/v1/hack/events/" + slug
	if r := a.at(t, "PUT", base+"/registration", map[string]any{"approval_required": true}, a.key(org)); r.status != 200 {
		t.Fatalf("settings: %d %s", r.status, r.body)
	}
	join := a.at(t, "GET", base, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	for _, p := range []person{approved, pending, rejected} {
		if r := a.at(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "Applicant"}, a.key(p)); r.status != 200 {
			t.Fatalf("join: %d %s", r.status, r.body)
		}
	}
	for _, tc := range []struct {
		p      person
		status string
	}{{approved, "approved"}, {rejected, "rejected"}} {
		if r := a.at(t, "POST", base+"/applications/"+a.userID(t, tc.p)+"/decision", map[string]string{"decision": tc.status}, a.key(org)); r.status != 200 {
			t.Fatalf("decision: %d %s", r.status, r.body)
		}
	}
	r := a.at(t, "POST", base+"/announcements", map[string]any{"title": "Update", "body": "Approved team information", "email_participants": true}, a.key(org))
	// org is email-shaped and never applied as a participant, so the organiser
	// copy adds a second delivery alongside the one approved applicant's.
	if r.status != 201 || r.json(t)["emails_queued"] != float64(2) || r.json(t)["organiser_copied"] != true {
		t.Fatalf("mail queue: %d %s", r.status, r.body)
	}
	a.hack.deliverContentMail(context.Background(), a.hack.mailer)
	if len(sink.notices) != 2 {
		t.Fatalf("recipients: %+v", sink.notices)
	}
	gotApproved, gotOrganiser := false, false
	for _, n := range sink.notices {
		gotApproved = gotApproved || n.to == approved.email
		gotOrganiser = gotOrganiser || n.to == org.email
	}
	if !gotApproved || !gotOrganiser {
		t.Fatalf("recipients: %+v", sink.notices)
	}
}

func (s *contentMailSink) SendRichNoticeCtx(_ context.Context, to, subject, body, html string) error {
	s.notices = append(s.notices, struct{ to, subject, body, html string }{to, subject, body, html})
	return nil
}
