package visitors

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/data/storetest"

	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

func TestRulesPeopleAuthorTracking(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rules   *json.RawMessage
		tracked bool
	}{
		{"ordinary_add", storetest.Set(ruleengine.Policy{Command: ruleengine.CmdAdd, Check: "true"}), true},
		{"identity_read", storetest.ReadSet(`visitor_id == auth.id`), true},
		{"owner_only", storetest.ReadSet(`auth.role == "owner"`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			column := "visitor_id"
			if tc.name == "identity_read" {
				column = "VISITOR_ID"
			}
			s, c, _ := storetest.Read(t, "CREATE TABLE entries(body TEXT,"+column+" TEXT);", tc.rules)
			storetest.Exec(t, s, fmt.Sprintf(`INSERT INTO entries VALUES('data','%s');`, c.OwnerID))
			if _, err := s.DB.Exec(`INSERT INTO site_visitor_signins(site_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, c.SiteID, c.OwnerID); err != nil {
				t.Fatal(err)
			}
			for _, ownerTools := range []bool{true, false} {
				c.Owner, c.OwnerOnSite = ownerTools, !ownerTools
				people, err := SitePeople(s, context.Background(), c)
				if err != nil || (len(people) == 1) != tc.tracked {
					t.Fatalf("people=%v err=%v", people, err)
				}
				if tc.tracked && (people[0].VisitorID != c.OwnerID || !people[0].HasData) {
					t.Fatal(people)
				}
			}
			c.Owner, c.OwnerOnSite = false, false
			if _, err := SitePeople(s, context.Background(), c); err == nil || err.(*data.Error).Code != "forbidden" {
				t.Fatalf("visitor people: %v", err)
			}
			if _, err := Visitors(s, context.Background(), VisitorsInput{Call: c, IDs: []string{c.OwnerID}}); err == nil || err.(*data.Error).Code != "forbidden" {
				t.Fatalf("visitor emails: %v", err)
			}
		})
	}
}
