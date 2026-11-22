package content

import (
	"testing"

	"github.com/vsriram/simple-host/products/hack/testdb"

	hacktest "github.com/vsriram/simple-host/products/hack/hacktest"

	"github.com/vsriram/simple-host/host/email"
)

func TestHackAnnouncementRequiresConfiguredEmail(t *testing.T) {
	testdb.IsolateQueueTest(t)
	a := newHackApp(t)
	a.hack.SetMailer(email.NewResendSender("", "test@example.com"))
	org := a.NewPerson(t, "content-no-mail")
	slug := hacktest.UniqueSlug()
	if r := a.CreateEvent(t, org, slug, nil); r.Status != 201 {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	r := a.At(t, "POST", "/v1/hack/events/"+slug+"/announcements", map[string]any{"title": "Hello", "body": "World", "email_participants": true}, a.Key(org))
	if r.Status != 503 || r.JSON(t)["code"] != "email_unavailable" {
		t.Fatalf("email unavailable: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "POST", "/v1/hack/events/"+slug+"/announcements", map[string]any{"title": "Hello", "body": "World"}, a.Key(org))
	if r.Status != 201 {
		t.Fatalf("web-only announcement: %d %s", r.Status, r.Body)
	}
}
