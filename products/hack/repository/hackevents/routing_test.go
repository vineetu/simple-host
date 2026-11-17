package hackevents

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/model"
	sites "github.com/vsriram/simple-host/host/repository/sites"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

func TestRoutingFactsInPlatformSnapshot(t *testing.T) {
	db, err := sql.Open("postgres", testdb.Fresh(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var uid, event string
	if err = db.QueryRow(`INSERT INTO users(username,handle) VALUES ('routing@example.com','routing-group') RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`INSERT INTO events(slug,account_id,title,organiser_name,contact_email,join_code,judge_code) VALUES ('routing-group',$1,'Routing fixture','Fixture','routing@example.com','private-join','private-judge') RETURNING id`, uid).Scan(&event); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO event_teams(event_id,slug,name,code) VALUES ($1,'site','Site','private-team')`, event); err != nil {
		t.Fatal(err)
	}
	table, _, err := sites.LoadSiteTable(context.Background(), db, LoadRoutingFacts)
	if err != nil {
		t.Fatal(err)
	}
	if len(table.Groups) != 1 || table.Groups[0] != (model.RouteGroup{ID: event, Slug: "routing-group", AccountID: uid}) || len(table.GroupSites) != 1 || table.GroupSites[0].Name != "site" {
		t.Fatalf("routing projection: %+v %+v", table.Groups, table.GroupSites)
	}
	root, err := LookupRoutingFacts(context.Background(), db, "routing-group", "")
	if err != nil || len(root.Groups) != 1 || len(root.Sites) != 0 {
		t.Fatalf("new group without a website: %+v %v", root, err)
	}
	if _, err = db.Exec(`INSERT INTO sites(user_id,name,active_version) VALUES ($1,'site',1)`, uid); err != nil {
		t.Fatal(err)
	}
	targeted, err := LookupRoutingFacts(context.Background(), db, "routing-group", "site")
	if err != nil || len(targeted.Sites) != 1 || len(targeted.GroupSites) != 1 || targeted.Sites[0].ActiveVersion != 1 {
		t.Fatalf("new team TLS projection: %+v %v", targeted, err)
	}
	if _, err := LookupRoutingFacts(context.Background(), db, "missing", "site"); err != sql.ErrNoRows {
		t.Fatalf("missing group: %v", err)
	}
	b, err := json.Marshal(table)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "private-") {
		t.Fatal("product credential entered routing table")
	}
}
