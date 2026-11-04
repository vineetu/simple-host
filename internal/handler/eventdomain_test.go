package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
)

type eventTestDNS struct {
	created, deleted []string
	failCreate       int
}

func (d *eventTestDNS) TakenNames(context.Context, string) (map[string]bool, error) {
	return map[string]bool{}, nil
}
func (d *eventTestDNS) CreateA(_ context.Context, _, label, _ string, ttl int) (string, error) {
	d.created = append(d.created, label)
	if ttl != 120 {
		return "", fmt.Errorf("unexpected TTL %d", ttl)
	}
	if d.failCreate == len(d.created) {
		return "", errors.New("create failed")
	}
	return fmt.Sprintf("record-%d", len(d.created)), nil
}
func (d *eventTestDNS) DeleteRecord(_ context.Context, _, id string) error {
	d.deleted = append(d.deleted, id)
	return nil
}

type eventProbeTransport struct{}

func (eventProbeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
}

func TestEventDomainWorkflow(t *testing.T) {
	a := newPrivateApp(t)
	p := a.newPerson(t, "event-owner")
	uid, _ := a.userID(t, p)
	dns := &eventTestDNS{}
	h := NewEventDomainHandler(a.database, dns, []string{"event-workflow.test"})
	h.probe.HTTP = &http.Client{Transport: eventProbeTransport{}}
	request := func(method, path, body string, user *db.User) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r = r.WithContext(auth.WithUser(r.Context(), user))
		w := httptest.NewRecorder()
		switch method {
		case http.MethodPost:
			h.claim(w, r)
		case http.MethodGet:
			h.list(w, r)
		case http.MethodDelete:
			r.SetPathValue("name", "meetup")
			h.release(w, r)
		}
		return w
	}
	owner := &db.User{ID: uid}
	claim := func() *httptest.ResponseRecorder {
		return request("POST", "/v1/events", `{"name":"meetup","ip":"8.8.8.8"}`, owner)
	}
	if w := claim(); w.Code != 201 {
		t.Fatalf("claim: %d %s", w.Code, w.Body.String())
	}
	if !reflect.DeepEqual(dns.created, []string{"meetup", "sites.meetup"}) {
		t.Fatalf("DNS creation order: %v", dns.created)
	}
	if ok, err := h.NameClaimed(context.Background(), "event-workflow.test", "meetup"); err != nil || !ok {
		t.Fatalf("claimed: %v %v", ok, err)
	}
	if w := request("GET", "/v1/events", "", owner); w.Code != 200 || !strings.Contains(w.Body.String(), `"domains":["event-workflow.test"]`) || !strings.Contains(w.Body.String(), `"host":"meetup.event-workflow.test"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	if w := claim(); w.Code != 201 {
		t.Fatalf("renew: %d %s", w.Code, w.Body.String())
	}
	if !reflect.DeepEqual(dns.deleted, []string{"record-1", "record-2"}) {
		t.Fatalf("renew cleanup: %v", dns.deleted)
	}
	if w := request("DELETE", "/v1/events/meetup", "", &db.User{ID: "another-user"}); w.Code != 404 || w.Body.String() != "{\"error\":\"not found\"}\n" {
		t.Fatalf("non-owner: %d %s", w.Code, w.Body.String())
	}
	if w := request("DELETE", "/v1/events/meetup", "", owner); w.Code != 204 {
		t.Fatalf("release: %d %s", w.Code, w.Body.String())
	}
	if ok, err := h.NameClaimed(context.Background(), "event-workflow.test", "meetup"); err != nil || ok {
		t.Fatalf("released: %v %v", ok, err)
	}
	// A half-created new claim removes its first record and its row.
	dns.failCreate = len(dns.created) + 2
	if w := claim(); w.Code != 502 || w.Body.String() != "{\"error\":\"could not create DNS records\"}\n" {
		t.Fatalf("partial claim: %d %s", w.Code, w.Body.String())
	}
	if ok, err := h.NameClaimed(context.Background(), "event-workflow.test", "meetup"); err != nil || ok {
		t.Fatalf("failed claim retained: %v %v", ok, err)
	}
	if got := dns.deleted[len(dns.deleted)-1]; got != "record-5" {
		t.Fatalf("partial DNS cleanup: %v", dns.deleted)
	}
	// Sweep removes both records before dropping the expired row.
	dns.failCreate = 0
	if w := claim(); w.Code != 201 {
		t.Fatalf("claim before sweep: %d %s", w.Code, w.Body.String())
	}
	if _, err := a.database.Exec(`UPDATE event_domains SET expires_at=now()-interval '1 second' WHERE name=$1 AND domain=$2`, "meetup", "event-workflow.test"); err != nil {
		t.Fatal(err)
	}
	h.sweepOnce(context.Background())
	var count int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_domains WHERE name=$1 AND domain=$2`, "meetup", "event-workflow.test").Scan(&count); err != nil || count != 0 {
		t.Fatalf("sweep: count=%d err=%v", count, err)
	}
	if got := dns.deleted[len(dns.deleted)-2:]; !reflect.DeepEqual(got, []string{"record-7", "record-8"}) {
		t.Fatalf("sweep DNS cleanup: %v", got)
	}
}
