package handler

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/email"
)

type contentMailSink struct {
	notices []struct{ to, subject, body string }
}

type failingContentSink struct {
	contentMailSink
	failedTo string
	failNext bool
}

func (s *failingContentSink) SendNotice(to, subject, body string) error {
	if s.failNext {
		s.failNext = false
		s.failedTo = to
		return errors.New("disposable delivery failure")
	}
	return s.contentMailSink.SendNotice(to, subject, body)
}

func (*contentMailSink) SendSignInCode(string, string, string) error { return nil }
func (s *contentMailSink) SendNotice(to, subject, body string) error {
	s.notices = append(s.notices, struct{ to, subject, body string }{to, subject, body})
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
	a.hack.deliverContentMail(context.Background())
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
	a.hack.deliverContentMail(context.Background())
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

func TestHackEntryReceiptOnce(t *testing.T) {
	isolateQueueTest(t)
	a := newHackApp(t)
	sink := &contentMailSink{}
	a.hack.SetMailer(sink)
	org, participant := a.newPerson(t, "receipt-org"), a.newPerson(t, "receipt-part")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	a.openEvent(t, org, slug)
	base := "/v1/hack/events/" + slug
	// Simplify entry rules to title only (slice 1's build preset otherwise
	// needs more than a title before an entry can be submitted) -- this test
	// only exercises the receipt mail, not the rules engine.
	if r := a.at(t, "PUT", base+"/entry-rules", map[string]any{"preset": "custom"}, a.key(org)); r.status != 200 {
		t.Fatalf("entry rules: %d %s", r.status, r.body)
	}
	join := a.at(t, "GET", base, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	if r := a.at(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "Pat"}, a.key(participant)); r.status != 200 {
		t.Fatalf("join: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", base+"/teams", map[string]string{"name": "Pandas"}, a.key(participant)); r.status != 201 {
		t.Fatalf("team: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", base+"/entry", map[string]string{"title": "First entry"}, a.key(participant)); r.status != 200 {
		t.Fatalf("entry: %d %s", r.status, r.body)
	}
	// The receipt is queued on submit, not on the first complete save
	// (section 4.1).
	if r := a.at(t, "POST", base+"/entry/submit", map[string]any{}, a.key(participant)); r.status != 200 {
		t.Fatalf("submit: %d %s", r.status, r.body)
	}
	a.hack.deliverContentMail(context.Background())
	if len(sink.notices) != 1 || !strings.Contains(sink.notices[0].subject, "entry received") {
		t.Fatalf("receipt: %+v", sink.notices)
	}
	if r := a.at(t, "PUT", base+"/entry", map[string]string{"title": "Edited entry"}, a.key(participant)); r.status != 200 {
		t.Fatalf("edit: %d %s", r.status, r.body)
	}
	a.hack.deliverContentMail(context.Background())
	if len(sink.notices) != 1 {
		t.Fatalf("duplicate receipt: %+v", sink.notices)
	}
}

func TestHackMailFailureDoesNotBlockOtherMessages(t *testing.T) {
	isolateQueueTest(t)
	a := newHackApp(t)
	sink := &failingContentSink{failNext: true}
	a.hack.SetMailer(sink)
	org, first, second := a.newPerson(t, "retry-org"), a.newPerson(t, "retry-first"), a.newPerson(t, "retry-second")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	a.openEvent(t, org, slug)
	base := "/v1/hack/events/" + slug
	// Simplify entry rules to title only, as TestHackEntryReceiptOnce does --
	// this test only needs a submitted entry to queue a receipt.
	if r := a.at(t, "PUT", base+"/entry-rules", map[string]any{"preset": "custom"}, a.key(org)); r.status != 200 {
		t.Fatalf("entry rules: %d %s", r.status, r.body)
	}
	join := a.at(t, "GET", base, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	for _, p := range []person{first, second} {
		if r := a.at(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "Participant"}, a.key(p)); r.status != 200 {
			t.Fatalf("join: %d %s", r.status, r.body)
		}
	}
	if r := a.at(t, "POST", base+"/teams", map[string]string{"name": "First team"}, a.key(first)); r.status != 201 {
		t.Fatalf("team: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", base+"/entry", map[string]string{"title": "First entry"}, a.key(first)); r.status != 200 {
		t.Fatalf("entry: %d %s", r.status, r.body)
	}
	// The receipt is queued on submit, not on the first complete save.
	if r := a.at(t, "POST", base+"/entry/submit", map[string]any{}, a.key(first)); r.status != 200 {
		t.Fatalf("submit: %d %s", r.status, r.body)
	}
	r := a.at(t, "POST", base+"/announcements", map[string]any{"title": "Notice", "body": "Hello teams", "email_participants": true}, a.key(org))
	// org is email-shaped and not a participant here, so the organiser copy
	// is a third delivery alongside first's and second's.
	if r.status != 201 || r.json(t)["emails_queued"] != float64(3) {
		t.Fatalf("announcement: %d %s", r.status, r.body)
	}
	a.hack.deliverContentMail(context.Background())
	if len(sink.notices) != 3 {
		t.Fatalf("later announcement and receipt blocked: %+v", sink.notices)
	}
	var gotReceipt, gotOther bool
	for _, n := range sink.notices {
		gotReceipt = gotReceipt || strings.Contains(n.subject, "entry received")
		gotOther = gotOther || (n.to != sink.failedTo && strings.Contains(n.subject, "Notice"))
	}
	if !gotReceipt || !gotOther {
		t.Fatalf("wrong first pass: failed=%s sent=%+v", sink.failedTo, sink.notices)
	}
	a.hack.deliverContentMail(context.Background())
	if len(sink.notices) != 3 {
		t.Fatalf("failed row retried before delay: %+v", sink.notices)
	}
	if _, err := a.database.Exec(`UPDATE event_announcement_deliveries SET attempted_at=now()-interval '2 minutes' WHERE sent_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	a.hack.deliverContentMail(context.Background())
	if len(sink.notices) != 4 || sink.notices[3].to != sink.failedTo {
		t.Fatalf("failed row not retried: %+v", sink.notices)
	}
	a.hack.deliverContentMail(context.Background())
	if len(sink.notices) != 4 {
		t.Fatalf("completed receipt sent twice: %+v", sink.notices)
	}
}

// Key-only accounts (no email on file) have a username with no "@" -- the
// same shape as Simple Hack's key-only test accounts. An announcement must
// report that truthfully instead of claiming it emailed anyone.
func TestHackAnnouncementEmailsZeroForKeyOnlyParticipants(t *testing.T) {
	isolateQueueTest(t)
	a := newHackApp(t)
	sink := &contentMailSink{}
	a.hack.SetMailer(sink)
	org, participant := a.newKeyOnlyPerson(t, "keyonly-org"), a.newKeyOnlyPerson(t, "keyonly-part")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	a.openEvent(t, org, slug)
	base := "/v1/hack/events/" + slug
	join := a.at(t, "GET", base, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	if r := a.at(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "Pat"}, a.key(participant)); r.status != 200 {
		t.Fatalf("join: %d %s", r.status, r.body)
	}
	// counts.emailable_participants (what the checkbox labels itself with) is
	// 0 even though counts.participants is 1.
	ev := a.at(t, "GET", base, nil, a.key(org)).json(t)
	organiser := ev["organiser"].(map[string]any)
	counts := organiser["counts"].(map[string]any)
	if counts["participants"] != float64(1) || counts["emailable_participants"] != float64(0) {
		t.Fatalf("counts: %v", counts)
	}
	r := a.at(t, "POST", base+"/announcements", map[string]any{"title": "Hi", "body": "Hello", "email_participants": true}, a.key(org))
	if r.status != 201 || r.json(t)["emails_queued"] != float64(0) || r.json(t)["organiser_copied"] != false {
		t.Fatalf("announcement: %d %s", r.status, r.body)
	}
	a.hack.deliverContentMail(context.Background())
	if len(sink.notices) != 0 {
		t.Fatalf("no one should have been emailed: %+v", sink.notices)
	}
	anns := a.at(t, "GET", base+"/announcements", nil, a.key(org)).json(t)["announcements"].([]any)
	if first, _ := anns[0].(map[string]any); first["emails_sent"] != float64(0) {
		t.Fatalf("emails_sent: %v", first)
	}
}

// The deliveries table's primary key is (announcement_id, user_id), so
// queuing the organiser's copy alongside the participant query can never
// duplicate a row for the same person -- exercised directly against
// db.CreateEventAnnouncement, since the real API can never put the posting
// organiser in the participant query too (one role per event per user).
func TestHackCreateEventAnnouncementNoDuplicateForSameUser(t *testing.T) {
	isolateQueueTest(t)
	a := newHackApp(t)
	org, participant := a.newPerson(t, "dup-org"), a.newPerson(t, "dup-part")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	a.openEvent(t, org, slug)
	base := "/v1/hack/events/" + slug
	join := a.at(t, "GET", base, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	if r := a.at(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "Pat"}, a.key(participant)); r.status != 200 {
		t.Fatalf("join: %d %s", r.status, r.body)
	}
	var eventID string
	if err := a.database.QueryRow(`SELECT id FROM events WHERE slug = $1`, slug).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	participantID := a.userID(t, participant)
	tx, err := a.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, queued, organiserCopied, err := db.CreateEventAnnouncement(context.Background(), tx, eventID, "Title", "Body", true, participantID)
	if err != nil {
		t.Fatal(err)
	}
	if queued != 1 {
		t.Fatalf("expected the shared user to be queued exactly once, got %d", queued)
	}
	if !organiserCopied {
		t.Fatal("expected organiserCopied true: the only queued row is that user")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
