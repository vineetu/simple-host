package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
)

// The retention rehearsal uses real files and HTTP serving, in addition to
// the clock/notice cases in hack_cleanup_test.go.
func TestHackArchiveCleanupRealSites(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "ret-org")
	participant := a.newPerson(t, "ret-team")
	slug := a.makeEvent(t, org)
	a.join(t, slug, participant, org)
	team, _ := a.startTeam(t, slug, "Retained team", participant)
	markReady(t, a.certDir, slug)
	key := a.teamKey(t, slug, participant)
	wantTS(t, "publish", a.deployTeam(t, team, key, "Keep this project"), http.StatusCreated, "")
	host := team + "." + slug + "." + tsDomain
	wantTS(t, "site before archive", a.on(t, "GET", host, "/", nil, ""), http.StatusOK, "")
	// A second event's actual files must survive the first event's cleanup.
	other := a.makeEvent(t, org)
	a.join(t, other, participant, org)
	otherTeam, _ := a.startTeam(t, other, "Other project", participant)
	markReady(t, a.certDir, other)
	wantTS(t, "other publish", a.deployTeam(t, otherTeam, a.teamKey(t, other, participant), "Other event intact"), http.StatusCreated, "")
	path := "/v1/hack/events/" + slug
	wantTS(t, "archive", a.api(t, "POST", path+"/stage", map[string]string{"stage": "archived"}, org.key), http.StatusOK, "")
	var closed time.Time
	var account string
	if err := a.database.QueryRow(`SELECT closed_at, account_id FROM events WHERE slug=$1`, slug).Scan(&closed, &account); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "archived site remains", a.on(t, "GET", host, "/", nil, ""), http.StatusOK, "")
	wantTS(t, "archived deploy refused", a.deployTeam(t, team, key, "Too late"), http.StatusConflict, "event_closed")
	mail := &replyMailer{}
	a.hack.SetMailer(mail)
	keep, warn := config.Active().EventSitesKeep, config.Active().EventRemovalWarn
	a.hack.runEventCleanup(context.Background(), closed.Add(keep-warn+time.Second))
	if len(mail.messages()) != 1 {
		t.Fatalf("warning count: %d", len(mail.messages()))
	}
	wantTS(t, "warning does not remove site", a.on(t, "GET", host, "/", nil, ""), http.StatusOK, "")
	a.hack.runEventCleanup(context.Background(), closed.Add(keep+time.Second))
	gone := a.on(t, "GET", host, "/", nil, "")
	if gone.status == http.StatusOK || strings.Contains(string(gone.body), "Keep this project") {
		t.Fatalf("removed project still public: %d", gone.status)
	}
	if _, err := db.GetDeletedSiteByUser(context.Background(), a.database, account, team); err != nil {
		t.Fatalf("not in Recently deleted: %v", err)
	}
	wantTS(t, "event page retained", a.on(t, "GET", slug+"."+tsDomain, "/", nil, ""), http.StatusOK, "")
	untouched := a.on(t, "GET", otherTeam+"."+other+"."+tsDomain, "/", nil, "")
	if untouched.status != http.StatusOK || !strings.Contains(string(untouched.body), "Other event intact") {
		t.Fatalf("unrelated event affected: %d", untouched.status)
	}
	a.hack.runEventCleanup(context.Background(), closed.Add(keep+2*time.Hour))
	if len(mail.messages()) != 1 {
		t.Fatal("duplicate warning after removal")
	}
}
