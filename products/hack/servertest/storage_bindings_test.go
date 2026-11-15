package servertest

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
	accountapp "github.com/vsriram/simple-host/internal/app/accounts"
	appstorage "github.com/vsriram/simple-host/internal/app/storage"
	"github.com/vsriram/simple-host/internal/extension"
	"github.com/vsriram/simple-host/internal/handler"
	platformtest "github.com/vsriram/simple-host/internal/server/servertest"
	"github.com/vsriram/simple-host/internal/testdb"
	"github.com/vsriram/simple-host/products/hack"
	"github.com/vsriram/simple-host/products/hack/app/storagebind"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

const bindingWebsite = "00000000-0000-4000-8000-000000000026"
const bindingPlainSite = "00000000-0000-4000-8000-000000000025"
const bindingOtherMember = "00000000-0000-4000-8000-000000000002"

func bindingExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func bindingDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	seedGroupKeys(t, db)
	bindingExec(t, db, `INSERT INTO sites(id,user_id,name) VALUES ($1,$2,$3)`, bindingWebsite, keyHolder, keyEvent)
	return db
}

func resetBindingFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	bindingExec(t, db, `UPDATE events SET stage='building', submission_deadline=now()+interval '1 day', taken_down_at=NULL;
 UPDATE event_teams SET deadline_override=NULL, site_taken_down_at=NULL;
 UPDATE sites SET deleted_at=NULL, suspended_at=NULL;`)
	bindingExec(t, db, `UPDATE event_members SET role='participant', team_id=$1, approval_status='approved' WHERE user_id=$2`, keyTeam, keyMember)
	bindingExec(t, db, `UPDATE event_members SET role='participant', team_id=$1, approval_status='approved' WHERE user_id=$2`, "00000000-0000-4000-8000-000000000012", bindingOtherMember)
}

func bindingValues(team, siteTeam, stage string, organiser, before bool) ruleengine.Values {
	text := func(s string) ruleengine.Value {
		if s == "" {
			return ruleengine.Null()
		}
		return ruleengine.Text(s)
	}
	teams := ruleengine.List()
	if team != "" {
		teams = ruleengine.List(team)
	}
	return ruleengine.Values{
		"auth.team": text(team), "auth.teams": teams, "auth.organiser": ruleengine.Bool(organiser),
		"event.team": text(siteTeam), "event.stage": text(stage), "event.before_deadline": ruleengine.Bool(before),
	}
}

func TestHackStorageBindingValues(t *testing.T) {
	db := bindingDB(t, testdb.Fresh(t))
	p := storagebind.Provider{DB: db}
	schema := ruleengine.BindingSchema{
		"auth.team": {Type: ruleengine.TypeText, Nullable: true}, "auth.teams": {Type: ruleengine.TypeTextList},
		"auth.organiser": {Type: ruleengine.TypeBool}, "event.team": {Type: ruleengine.TypeText, Nullable: true},
		"event.stage": {Type: ruleengine.TypeText, Nullable: true}, "event.before_deadline": {Type: ruleengine.TypeBool},
	}
	if !reflect.DeepEqual(p.Schema(), schema) {
		t.Fatalf("schema = %#v", p.Schema())
	}
	if _, err := appstorage.MergeBindingSchema(false, p); err != nil {
		t.Fatal(err)
	}
	needed := []string{"auth.team", "auth.teams", "auth.organiser", "event.team", "event.stage", "event.before_deadline"}
	for _, tc := range []struct {
		name, site, user, change string
		want                     ruleengine.Values
	}{
		{"teammate before", keySite, keyMember, "", bindingValues("seed-live", "seed-live", "building", false, true)},
		{"teammate after", keySite, keyMember, `UPDATE events SET submission_deadline=now()-interval '1 day'`, bindingValues("seed-live", "seed-live", "building", false, false)},
		{"extended before", keySite, keyMember, `UPDATE events SET submission_deadline=now()-interval '1 day'; UPDATE event_teams SET deadline_override=now()+interval '1 day'`, bindingValues("seed-live", "seed-live", "building", false, true)},
		{"extended after", keySite, keyMember, `UPDATE events SET submission_deadline=now()-interval '2 days'; UPDATE event_teams SET deadline_override=now()-interval '1 day'`, bindingValues("seed-live", "seed-live", "building", false, false)},
		{"override cannot shorten", keySite, keyMember, `UPDATE event_teams SET deadline_override=now()-interval '1 day'`, bindingValues("seed-live", "seed-live", "building", false, true)},
		{"no deadline", keySite, keyMember, `UPDATE events SET submission_deadline=NULL`, bindingValues("seed-live", "seed-live", "building", false, true)},
		{"override without event deadline", keySite, keyMember, `UPDATE events SET submission_deadline=NULL; UPDATE event_teams SET deadline_override=now()-interval '1 day'`, bindingValues("seed-live", "seed-live", "building", false, true)},
		{"pending", keySite, keyMember, `UPDATE event_members SET approval_status='pending' WHERE role='participant'`, bindingValues("", "seed-live", "building", false, true)},
		{"organiser website", bindingWebsite, keyOrganiser, "", bindingValues("", "", "building", true, true)},
		{"organiser archived", bindingWebsite, keyOrganiser, `UPDATE events SET stage='archived'`, bindingValues("", "", "archived", true, true)},
		{"website deadline has no team override", bindingWebsite, keyOrganiser, `UPDATE events SET submission_deadline=now()-interval '1 day'; UPDATE event_teams SET deadline_override=now()+interval '1 day'`, bindingValues("", "", "building", true, false)},
		{"other team", keySite, bindingOtherMember, "", bindingValues("seed-trash", "seed-live", "building", false, true)},
		{"judge", keySite, keyMember, `UPDATE event_members SET role='judge',team_id=NULL WHERE role='participant'`, bindingValues("", "seed-live", "building", false, true)},
		{"anonymous", keySite, "", "", bindingValues("", "seed-live", "building", false, true)},
		{"not a member", keySite, keyHolder, "", bindingValues("", "seed-live", "building", false, true)},
		{"no event", bindingPlainSite, keyMember, "", bindingValues("", "", "", false, false)},
		{"take downs remain gates", keySite, keyMember, `UPDATE events SET taken_down_at=now(); UPDATE event_teams SET site_taken_down_at=now(); UPDATE sites SET suspended_at=now()`, bindingValues("seed-live", "seed-live", "building", false, true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetBindingFixture(t, db)
			if tc.change != "" {
				bindingExec(t, db, tc.change)
			}
			// Only the stored site determines the event, not the other call fields.
			got, err := p.Values(context.Background(), appstorage.BindingCall{SiteID: tc.site, UserID: tc.user, OwnerID: "ignored", SiteName: "ignored"}, needed)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("values = %#v, %v; want %#v", got, err, tc.want)
			}
		})
	}
	resetBindingFixture(t, db)
	got, err := p.Values(context.Background(), appstorage.BindingCall{SiteID: keySite}, []string{"event.stage"})
	if err != nil || !reflect.DeepEqual(got, ruleengine.Values{"event.stage": ruleengine.Text("building")}) {
		t.Fatalf("needed subset: %#v, %v", got, err)
	}
}

// This shared test stays when D2-S9 switches the handler callers to the provider.
func TestHackOwnerOnSiteParity(t *testing.T) {
	db := bindingDB(t, testdb.Fresh(t))
	p := storagebind.Provider{DB: db}
	for _, tc := range []struct {
		name, site, user, change string
		want                     bool
	}{
		{"teammate", keySite, keyMember, "", true},
		{"draft", keySite, keyMember, `UPDATE events SET stage='draft'`, true},
		{"open", keySite, keyMember, `UPDATE events SET stage='open'`, true},
		{"closed", keySite, keyMember, `UPDATE events SET stage='closed'`, true},
		{"judging", keySite, keyMember, `UPDATE events SET stage='judging'`, false},
		{"results", keySite, keyMember, `UPDATE events SET stage='results'`, false},
		{"archived", keySite, keyMember, `UPDATE events SET stage='archived'`, false},
		{"deadline", keySite, keyMember, `UPDATE events SET submission_deadline=now()-interval '1 day'`, false},
		{"extended", keySite, keyMember, `UPDATE events SET submission_deadline=now()-interval '1 day'; UPDATE event_teams SET deadline_override=now()+interval '1 day'`, true},
		{"extension expired", keySite, keyMember, `UPDATE events SET submission_deadline=now()-interval '2 days'; UPDATE event_teams SET deadline_override=now()-interval '1 day'`, false},
		{"no deadline", keySite, keyMember, `UPDATE events SET submission_deadline=NULL`, true},
		{"pending", keySite, keyMember, `UPDATE event_members SET approval_status='pending' WHERE role='participant'`, false},
		{"judge", keySite, keyMember, `UPDATE event_members SET role='judge',team_id=NULL WHERE role='participant'`, false},
		{"no team", keySite, keyMember, `UPDATE event_members SET team_id=NULL WHERE role='participant'`, false},
		{"other team", keySite, bindingOtherMember, "", false},
		{"not a member", keySite, keyHolder, "", false},
		{"ordinary site", bindingPlainSite, keyMember, "", false},
		{"deleted team site", keySite, keyMember, `UPDATE sites SET deleted_at=now()`, false},
		{"suspended team site", keySite, keyMember, `UPDATE sites SET suspended_at=now()`, false},
		{"team taken down", keySite, keyMember, `UPDATE event_teams SET site_taken_down_at=now()`, false},
		{"event taken down team", keySite, keyMember, `UPDATE events SET taken_down_at=now()`, false},
		{"organiser on team site", keySite, keyOrganiser, "", false},
		{"organiser website", bindingWebsite, keyOrganiser, "", true},
		{"organiser judging", bindingWebsite, keyOrganiser, `UPDATE events SET stage='judging'`, true},
		{"organiser results", bindingWebsite, keyOrganiser, `UPDATE events SET stage='results'`, true},
		{"organiser deadline passed", bindingWebsite, keyOrganiser, `UPDATE events SET submission_deadline=now()-interval '1 day'`, true},
		{"organiser archived", bindingWebsite, keyOrganiser, `UPDATE events SET stage='archived'`, false},
		{"event taken down website", bindingWebsite, keyOrganiser, `UPDATE events SET taken_down_at=now()`, false},
		{"deleted website", bindingWebsite, keyOrganiser, `UPDATE sites SET deleted_at=now()`, false},
		{"suspended website", bindingWebsite, keyOrganiser, `UPDATE sites SET suspended_at=now()`, false},
		{"participant website", bindingWebsite, keyMember, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetBindingFixture(t, db)
			if tc.change != "" {
				bindingExec(t, db, tc.change)
			}
			old, oldErr := hackevents.HackSiteOwnerOnSite(context.Background(), db, tc.site, tc.user)
			got, err := p.OwnerOnSite(context.Background(), tc.site, tc.user)
			if err != nil || oldErr != nil || got != old || got != tc.want {
				t.Fatalf("provider %v, %v; old %v, %v; want %v", got, err, old, oldErr, tc.want)
			}
		})
	}
}

func TestHackStorageBindingsAfterBuildAndDatabaseError(t *testing.T) {
	t.Setenv("STORAGE_RULES", "on")
	cfg := platformtest.Config(t, testdb.Fresh(t))
	cfg.SiteHosts = "canonical"
	cfg.PersonHosts = "canonical"
	db := bindingDB(t, cfg.DBDSN)
	resetBindingFixture(t, db)
	ext := hack.Extension()
	wire := ext.Wire
	var site *handler.SiteHandler
	ext.Wire = func(d extension.Deps) (extension.Parts, error) { site = d.Site; return wire(d) }
	t.Cleanup(func() { accountapp.SetGroupHooks(accountapp.GroupHooks{}) })
	app := platformtest.Build(t, cfg, []*extension.Extension{ext})
	base := "/v1/sites/seed-live/storage"
	request := func(method, path, body string, owner bool) *httptest.ResponseRecorder {
		t.Helper()
		host := "seed-live.seed-rehearsal." + cfg.SiteDomain
		if owner {
			host = cfg.SiteDomain
		}
		r := httptest.NewRequest(method, "https://"+host+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if owner {
			r.Header.Set("X-API-Key", keyRaw)
		}
		w := httptest.NewRecorder()
		app.Handler.ServeHTTP(w, r)
		return w
	}
	check := func(method, path, body string, owner bool, want int, code string) {
		t.Helper()
		w := request(method, path, body, owner)
		if w.Code != want || code != "" && !strings.Contains(w.Body.String(), `"code":"`+code+`"`) {
			t.Fatalf("%s %s: %d %s; want %d %s", method, path, w.Code, w.Body.String(), want, code)
		}
	}
	check("PUT", base+"/resources/bindings", `{"kind":"sqlite","rules":{"version":1,"policies":[{"command":"read","using":"event.before_deadline"}]}}`, true, 201, "")
	check("POST", base+"/sqlite/bindings/schema", `{"sql":"CREATE TABLE entries(id INTEGER PRIMARY KEY, body TEXT)"}`, true, 200, "")
	path := base + "/sqlite/bindings/tables/entries/rows"
	check("GET", path, "", false, 200, "")
	bindingExec(t, db, `UPDATE events SET submission_deadline=now()-interval '1 day'`)
	check("GET", path, "", false, 403, "forbidden")
	bindingExec(t, db, `UPDATE events SET submission_deadline=NULL`)
	check("GET", path, "", false, 200, "")
	// Only the real provider's connection is broken; request metadata still loads.
	broken, err := sql.Open("postgres", cfg.DBDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := broken.Close(); err != nil {
		t.Fatal(err)
	}
	site.SetStorageBindings(storagebind.Provider{DB: broken})
	check("GET", path, "", false, 500, "internal_error")
}
