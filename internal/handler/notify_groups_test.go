package handler

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	notifyapp "github.com/vsriram/simple-host/internal/app/notifications"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	"github.com/vsriram/simple-host/internal/testdb"
)

type failingNotificationLabels struct {
	notifyapp.GroupSites
	err error
}

func (f failingNotificationLabels) Labels(context.Context, sqlkit.Querier, []string) (map[string]string, error) {
	return nil, f.err
}

func TestNotificationGroupsNilAndErrors(t *testing.T) {
	s, _ := notificationFixture(t)
	id := "sub_groups_test"
	notificationSQL(t, s, `INSERT INTO site_notifications(id,site_id,owner_id,source,url,events,secret,kind,subscriber_role) VALUES($1,$2,$3,'owner','https://example.invalid/push','{record.added}','', 'web-push','visitor')`, id, s.sid, s.ownerID)
	database, recorded := testdb.OpenRecording(os.Getenv("DB_DSN"))
	t.Cleanup(func() { database.Close() })
	database.SetMaxOpenConns(1) // enrichment must run after listing rows close
	svc := notifyapp.New(database, nil)
	ctx := context.Background()
	n, err := svc.GetNotification(ctx, id)
	if err != nil || n.Event != "" {
		t.Fatalf("nil hook get: label %q, error %v", n.Event, err)
	}
	listings := []struct {
		name string
		list func() ([]model.Notification, error)
	}{
		{"notifications", func() ([]model.Notification, error) { return svc.ListNotifications(ctx, s.ownerID, s.sid) }},
		{"push", func() ([]model.Notification, error) { return svc.ListPushDevices(ctx, s.sid) }},
		{"visitor push", func() ([]model.Notification, error) { return svc.ListVisitorPushDevices(ctx, s.sid, s.ownerID) }},
	}
	for _, test := range listings {
		rows, err := test.list()
		if err != nil || len(rows) != 1 || rows[0].Event != "" {
			t.Fatalf("nil %s: rows %v, error %v", test.name, rows, err)
		}
	}
	if _, err := svc.GroupNotificationSite(ctx, s.ownerID, "site", ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("nil site: %v", err)
	}
	if ok, err := svc.GroupNotificationAllowed(ctx, s.sid, s.ownerID, true); ok || err != nil {
		t.Fatalf("nil allowed: %v %v", ok, err)
	}
	if ok, err := svc.GroupNotificationContextExists(ctx, s.ownerID, "site", "", ""); ok || err != nil {
		t.Fatalf("nil context: %v %v", ok, err)
	}
	if _, err := svc.GroupLabel(ctx, s.ownerID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("nil label: %v", err)
	}
	_, _, err = svc.SaveNotification(ctx, model.Notification{ID: "sub_refused", SiteID: s.sid, OwnerID: s.ownerID, Hack: true}, 10, nil, 7, model.NotificationSecrets{})
	if !errors.Is(err, model.ErrNotificationAccess) {
		t.Fatalf("nil save: %v", err)
	}
	// Use the inventory, including product tables without an event_/hack_ prefix.
	objects, err := os.ReadFile("../../scripts/split/hack-objects.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(objects), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line == "" {
			continue
		}
		object := strings.TrimSpace(strings.SplitN(line, "|", 3)[1])
		// "events" also names a platform notification column. Check table
		// positions in the recording, not that unqualified column name.
		table := regexp.MustCompile(`(?i)\b(?:FROM|JOIN|UPDATE|INTO|TABLE)\s+(?:public\.)?"?` + regexp.QuoteMeta(object) + `"?\b`)
		for _, statement := range recorded.Naming(object) {
			if table.MatchString(statement) {
				t.Fatalf("platform queried product table %s", object)
			}
		}
	}
	failure := errors.New("labels unavailable")
	svc.WithGroupSites(failingNotificationLabels{err: failure})
	for _, test := range listings {
		if _, err := test.list(); !errors.Is(err, failure) {
			t.Fatalf("%s error: %v", test.name, err)
		}
	}
	if _, err := svc.GetNotification(ctx, id); !errors.Is(err, failure) {
		t.Fatalf("get error: %v", err)
	}
	s.a.sites.SetNotificationGroups(failingNotificationLabels{err: failure})
	r := s.a.at(t, "GET", pcSiteDomain, "/v1/notifications", nil, s.key)
	if r.status != 500 || !strings.Contains(string(r.body), "internal_error") {
		t.Fatalf("listing error path: %d %s", r.status, r.body)
	}
}
