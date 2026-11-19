package servertest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	accountapp "github.com/vsriram/simple-host/host/app/accounts"
	appstorage "github.com/vsriram/simple-host/host/app/storage"
	"github.com/vsriram/simple-host/host/extension"
	"github.com/vsriram/simple-host/host/handler"
	"github.com/vsriram/simple-host/host/server"
	platformtest "github.com/vsriram/simple-host/host/server/servertest"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
	"github.com/vsriram/simple-host/products/hack"
	"github.com/vsriram/simple-host/products/hack/app/storagebind"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/testdb"
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

func TestHackBuildRequiresStorageBindings(t *testing.T) {
	cfg := platformtest.Config(t, testdb.Fresh(t))
	ext := hack.Extension()
	wire := ext.Wire
	ext.Wire = func(d extension.Deps) (extension.Parts, error) {
		parts, err := wire(d)
		parts.Hooks.Storage = nil
		return parts, err
	}
	t.Cleanup(func() { accountapp.SetGroupHooks(accountapp.GroupHooks{}) })
	platformtest.RecordProduct(t, cfg, []*extension.Extension{ext})
	app, err := server.Build(cfg, []*extension.Extension{ext})
	if app != nil {
		app.Close()
		t.Fatal("Hack started without storage bindings")
	}
	if err == nil || !strings.Contains(err.Error(), "requires storage bindings") {
		t.Fatalf("missing storage bindings error: %v", err)
	}
}

func TestHackStorageBindingValues(t *testing.T) {
	db := bindingDB(t, testdb.Fresh(t))
	p := storagebind.Provider{DB: db}
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

func TestStorageOwnerOnSiteEdgeCases(t *testing.T) {
	a := newTeamSiteApp(t)
	org, member, other := a.newPerson(t, "binding-org"), a.newPerson(t, "binding-member"), a.newPerson(t, "binding-other")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	team, _ := a.startTeam(t, slug, "Bindings", member)
	markReady(t, a.certDir, slug)
	wantTS(t, "team site", a.deployTeam(t, team, a.teamKey(t, slug, member), "team"), 201, "")
	wantTS(t, "event site", a.api(t, "PUT", "/v1/hack/events/"+slug+"/website/files?create=1", deployBody("event"), org.key), 201, "")
	var eid string
	if err := a.database.QueryRow(`SELECT id FROM events WHERE slug=$1`, slug).Scan(&eid); err != nil {
		t.Fatal(err)
	}
	teamSite, eventSite := hackSiteID(t, a, slug, team), hackSiteID(t, a, slug, eid)
	mid, oid, otherID := a.uid(t, member), a.uid(t, org), a.uid(t, other)
	type change struct {
		query string
		args  []any
	}
	event := func(set string) change { return change{`UPDATE events SET ` + set + ` WHERE id=$1`, []any{eid}} }
	teamChange := func(set string) change {
		return change{`UPDATE event_teams SET ` + set + ` WHERE event_id=$1 AND slug=$2`, []any{eid, team}}
	}
	memberChange := func(set string) change {
		return change{`UPDATE event_members SET ` + set + ` WHERE event_id=$1 AND user_id=$2`, []any{eid, mid}}
	}
	siteChange := func(id, set string) change { return change{`UPDATE sites SET ` + set + ` WHERE id=$1`, []any{id}} }
	cases := []struct {
		name, site, user string
		want             bool
		changes          []change
	}{
		{"approved teammate", teamSite, mid, true, nil},
		{"unrelated visitor", teamSite, otherID, false, nil},
		{"organiser on team site", teamSite, oid, false, nil},
		{"participant on event website", eventSite, mid, false, nil},
		{"organiser on event website", eventSite, oid, true, nil},
		{"missing site", "00000000-0000-0000-0000-000000000000", mid, false, nil},
		{"no event", teamSite, mid, false, []change{siteChange(teamSite, `user_id='`+otherID+`'`)}},
		{"no matching team", teamSite, mid, false, []change{siteChange(teamSite, `name='unmatched'`)}},
		{"member removed", teamSite, mid, false, []change{{`DELETE FROM event_members WHERE event_id=$1 AND user_id=$2`, []any{eid, mid}}}},
		{"no team", teamSite, mid, false, []change{memberChange(`team_id=NULL`)}},
		{"judge", teamSite, mid, false, []change{memberChange(`team_id=NULL,role='judge'`)}},
		{"pending", teamSite, mid, false, []change{memberChange(`approval_status='pending'`)}},
		{"rejected", teamSite, mid, false, []change{memberChange(`approval_status='rejected'`)}},
		{"team site taken down", teamSite, mid, false, []change{teamChange(`site_taken_down_at=now()`)}},
		{"deadline passed", teamSite, mid, false, []change{event(`submission_deadline=now()-interval '1 hour'`)}},
		{"future deadline", teamSite, mid, true, []change{event(`submission_deadline=now()+interval '1 hour'`)}},
		{"future override", teamSite, mid, true, []change{event(`submission_deadline=now()-interval '1 hour'`), teamChange(`deadline_override=now()+interval '1 hour'`)}},
		{"expired override", teamSite, mid, false, []change{event(`submission_deadline=now()-interval '2 hours'`), teamChange(`deadline_override=now()-interval '1 hour'`)}},
		{"earlier override", teamSite, mid, true, []change{event(`submission_deadline=now()+interval '1 hour'`), teamChange(`deadline_override=now()-interval '1 hour'`)}},
		{"override without event deadline", teamSite, mid, true, []change{event(`submission_deadline=NULL`), teamChange(`deadline_override=now()-interval '1 hour'`)}},
		{"organiser past deadline", eventSite, oid, true, []change{event(`submission_deadline=now()-interval '1 hour'`)}},
	}
	for _, target := range []struct{ name, site, user string }{{"team", teamSite, mid}, {"website", eventSite, oid}} {
		for _, state := range []string{"draft", "open", "building", "closed", "judging", "results", "archived"} {
			want := state != "archived" && (target.name == "website" || state != "judging" && state != "results")
			cases = append(cases, struct {
				name, site, user string
				want             bool
				changes          []change
			}{target.name + " stage " + state, target.site, target.user, want, []change{event(`stage='` + state + `'`)}})
		}
		for _, c := range []struct {
			name   string
			change change
		}{{"event taken down", event(`taken_down_at=now()`)}, {"site deleted", siteChange(target.site, `deleted_at=now()`)}, {"site suspended", siteChange(target.site, `suspended_at=now()`)}} {
			cases = append(cases, struct {
				name, site, user string
				want             bool
				changes          []change
			}{target.name + " " + c.name, target.site, target.user, false, []change{c.change}})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := a.database.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for _, c := range tc.changes {
				if _, err := tx.Exec(c.query, c.args...); err != nil {
					t.Fatal(err)
				}
			}
			old, oldErr := hackevents.HackSiteOwnerOnSite(context.Background(), tx, tc.site, tc.user)
			got, err := (storagebind.Provider{DB: tx}).OwnerOnSite(context.Background(), tc.site, tc.user)
			if oldErr != nil || err != nil || old != tc.want || got != old {
				t.Fatalf("provider=%v/%v old=%v/%v want=%v", got, err, old, oldErr, tc.want)
			}
		})
	}
	t.Run("database error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		old, oldErr := hackevents.HackSiteOwnerOnSite(ctx, a.database, teamSite, mid)
		got, err := (storagebind.Provider{DB: a.database}).OwnerOnSite(ctx, teamSite, mid)
		if got || old || !errors.Is(err, context.Canceled) || !errors.Is(oldErr, context.Canceled) {
			t.Fatalf("provider=%v/%v old=%v/%v", got, err, old, oldErr)
		}
	})
}

type countedStorageBindings struct {
	appstorage.BindingProvider
	calls int
	fail  bool
}

func (p *countedStorageBindings) OwnerOnSite(ctx context.Context, site, user string) (bool, error) {
	p.calls++
	if p.fail {
		return true, errors.New("owner lookup failed")
	}
	return p.BindingProvider.OwnerOnSite(ctx, site, user)
}

// Use the actual Build wiring, then exercise both the HTTP dispatcher and the
// gated upstream installed on the connector. Owner-on-site is a visitor-session
// capability; MCP's owner credentials do not carry that session.
func TestStorageBindingsInstalledAfterBuild(t *testing.T) {
	cfg := platformtest.Config(t, testdb.Fresh(t))
	cfg.SiteDomain = tsDomain
	cfg.ContentHost = "sites." + tsDomain
	cfg.PublicBaseURL = "https://" + tsDomain
	cfg.PersonHosts = "canonical"
	cfg.SiteHosts = "canonical"
	cfg.SiteCertDir = ""
	a := &teamSiteApp{}
	ext := hack.Extension()
	wire := ext.Wire
	var provider *countedStorageBindings
	ext.Wire = func(d extension.Deps) (extension.Parts, error) {
		parts, err := wire(d)
		if parts.Hooks.Storage == nil {
			t.Fatal("Hack extension omitted storage bindings")
		}
		provider = &countedStorageBindings{BindingProvider: parts.Hooks.Storage}
		parts.Hooks.Storage = provider
		a.database = d.DB
		a.sites = d.Site
		a.conn = d.Connector
		a.disk = d.Disk
		return parts, err
	}
	app := platformtest.Build(t, cfg, []*extension.Extension{ext})
	a.mux = app.Mux
	a.srv = httptest.NewServer(app.Handler)
	t.Cleanup(a.srv.Close)
	httpServer := a.srv
	inProcess := httptest.NewServer(a.conn.MCPUpstreamForTest())
	t.Cleanup(inProcess.Close)
	org, member := a.newPerson(t, "wired-org"), a.newPerson(t, "wired-member")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	team, _ := a.startTeam(t, slug, "Wired", member)
	key := a.teamKey(t, slug, member)
	wantTS(t, "deploy", a.deployTeam(t, team, key, "wired"), 201, "")
	base := "/v1/sites/" + team + "/storage"
	wantTS(t, "private resource", a.api(t, "PUT", base+"/resources/private", map[string]any{"kind": "kv", "rules": json.RawMessage(privateRules)}, key), 201, "")
	wantTS(t, "private value", a.api(t, "PUT", base+"/kv/private/keys/key", `{"value":"private value"}`, key), 200, "")
	sid := hackSiteID(t, a, slug, team)
	host := team + "." + slug + "." + tsDomain
	asMember := hackSession(t, a, a.uid(t, member), sid, host)
	for _, fail := range []bool{false, true} {
		provider.fail = fail
		for _, transport := range []string{"HTTP", "in-process"} {
			if transport == "in-process" {
				a.srv = inProcess
			} else {
				a.srv = httpServer
			}
			before := provider.calls
			got := asMember("GET", base+"/kv/private/keys/key", nil)
			// Rules ask twice: the request's owner-on-site lookup, then the
			// fresh re-check inside the read; a failed lookup is never owner,
			// so there is nothing to re-check.
			want, calls := 200, 2
			if fail {
				want, calls = 403, 1
			}
			if got.status != want || provider.calls != before+calls {
				t.Fatalf("%s storage fail=%v: %d calls=%d %s", transport, fail, got.status, provider.calls-before, got.body)
			}
			before = provider.calls
			me := asMember("GET", "/v1/sites/"+team+"/me", nil)
			if me.status != 200 || (me.json(t)["site_owner"] == true) == fail || provider.calls != before+1 {
				t.Fatalf("%s me fail=%v: calls=%d %s", transport, fail, provider.calls-before, me.body)
			}
		}
		a.srv = httpServer
		before := provider.calls
		page := asMember("GET", "/", nil)
		guarded := page.header.Get("X-Frame-Options") == "SAMEORIGIN" && strings.Contains(page.header.Get("Content-Security-Policy"), "frame-ancestors 'self'")
		if page.status != 200 || guarded == fail || provider.calls != before+1 {
			t.Fatalf("frame fail=%v: %d calls=%d headers=%v", fail, page.status, provider.calls-before, page.header)
		}
	}
}
