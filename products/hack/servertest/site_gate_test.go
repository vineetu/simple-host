package servertest

import (
	"context"
	"database/sql"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/app/sites"
	"github.com/vsriram/simple-host/host/auth"
	"github.com/vsriram/simple-host/host/extension"
	"github.com/vsriram/simple-host/host/model"
	accountrepo "github.com/vsriram/simple-host/host/repository/accounts"
	siterepo "github.com/vsriram/simple-host/host/repository/sites"
	platformtest "github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/products/hack"
	"github.com/vsriram/simple-host/products/hack/app/teams"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

type siteGateFunc func(context.Context, *sql.Tx, *model.User, string) *sites.GateError

func (f siteGateFunc) Check(ctx context.Context, tx *sql.Tx, user *model.User, name string) *sites.GateError {
	return f(ctx, tx, user, name)
}

func (f siteGateFunc) Suspension(context.Context, *sql.Tx, siterepo.Site) (string, string, error) {
	return "", "", nil
}

func TestSiteGateInstalledAfterBuild(t *testing.T) {
	cfg := platformtest.Config(t, testdb.Fresh(t))
	cfg.PersonHosts, cfg.SiteHosts = "canonical", "canonical"
	ext := hack.Extension()
	wire := ext.Wire
	var db *sql.DB
	calls := 0
	refuse := false
	ext.Wire = func(d extension.Deps) (extension.Parts, error) {
		parts, err := wire(d)
		if err != nil {
			return parts, err
		}
		db = d.DB
		gate := parts.Hooks.GroupGate
		if gate == nil {
			t.Fatal("Hack returned no site gate")
		}
		parts.Hooks.GroupGate = siteGateFunc(func(ctx context.Context, tx *sql.Tx, user *model.User, name string) *sites.GateError {
			calls++
			if tx == nil {
				t.Fatal("gate has no transaction")
			}
			if refuse {
				return &sites.GateError{Code: "event_closed", Message: "this event has ended"}
			}
			return gate.Check(ctx, tx, user, name)
		})
		return parts, nil
	}
	app := platformtest.Build(t, cfg, []*extension.Extension{ext})
	key, _ := auth.GenerateAPIKey()
	if _, err := accountrepo.CreateUser(context.Background(), db, "gate-wiring@example.com", key, false); err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string, mcp bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, cfg.PublicBaseURL+path, strings.NewReader(body))
		r.Header.Set("X-API-Key", key)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("MCP-Protocol-Version", "2025-06-18")
		w := httptest.NewRecorder()
		if mcp {
			app.Mux.ServeHTTP(w, r)
		} else {
			app.Handler.ServeHTTP(w, r)
		}
		return w
	}
	w := request("POST", "/v1/hack/events", `{"slug":"gate-wiring","title":"Gate event","organiser_name":"Organiser","contact_email":"organiser@example.com","purpose":"testing","expected_participants":10,"starts_at":"2026-10-01","time_zone":"UTC"}`, false)
	if w.Code != 201 {
		t.Fatalf("create event: %d %s", w.Code, w.Body.String())
	}
	var eventID string
	if err := db.QueryRow(`SELECT id FROM events WHERE slug='gate-wiring'`).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	for _, failure := range []bool{false, true} {
		refuse = failure
		for _, transport := range []string{"REST", "MCP"} {
			before := calls
			if transport == "REST" {
				w = request("PUT", "/v1/sites/"+eventID+"/files?create=1&owner=gate-wiring", `{"files":{"index.html":"gate test"}}`, false)
			} else {
				w = request("POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"update_site","arguments":{"site":"`+eventID+`","owner":"gate-wiring","files":{"index.html":"gate test"}}}}`, true)
			}
			if calls <= before {
				t.Fatalf("%s did not reach gate: %d %s", transport, w.Code, w.Body.String())
			}
			if failure {
				if !strings.Contains(w.Body.String(), "this event has ended") || (transport == "REST" && w.Code != 409) || (transport == "MCP" && !strings.Contains(w.Body.String(), `"isError":true`)) {
					t.Fatalf("%s refusal: %d %s", transport, w.Code, w.Body.String())
				}
			} else if w.Code/100 != 2 || strings.Contains(w.Body.String(), `"isError":true`) {
				t.Fatalf("%s publish: %d %s", transport, w.Code, w.Body.String())
			}
		}
	}
	var versions int
	if err := db.QueryRow(`SELECT count(*) FROM versions v JOIN sites s ON s.id=v.site_id JOIN events e ON e.account_id=s.user_id WHERE e.slug='gate-wiring'`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 2 {
		t.Fatalf("refused writes created versions: %d", versions)
	}
}

func TestSiteGateRechecksDuringWrite(t *testing.T) {
	for _, operation := range []string{"create", "update", "rollback"} {
		// Removal from a team takes effect on the next request; a stage change
		// reaches a write already let in.
		for _, change := range []string{"stage"} {
			t.Run(operation+"/"+change, func(t *testing.T) {
				a := newTeamSiteApp(t)
				org, member := a.newPerson(t, "org"), a.newPerson(t, "member")
				slug := a.makeEvent(t, org)
				a.join(t, slug, member, org)
				team, _ := a.startTeam(t, slug, "Gate team", member)
				key := member.key
				markReady(t, a.certDir, slug)
				if operation != "create" {
					wantTS(t, "first deploy", a.deployTeam(t, slug, team, key, "v1"), 201, "")
					wantTS(t, "second deploy", a.deployTeam(t, slug, team, key, "v2"), 200, "")
				}
				gate := teams.SiteGate{TeamSitesReady: a.sites.TeamSitesReady, RequestCert: a.sites.RequestSiteCert, PaperSite: a.sites.RecordGateSite, PaperState: a.sites.RecordGateState}
				reached, resume := make(chan struct{}), make(chan struct{})
				a.sites.SetGroupGate(siteGateFunc(func(ctx context.Context, tx *sql.Tx, user *model.User, name string) *sites.GateError {
					close(reached)
					<-resume
					return gate.Check(ctx, tx, user, name)
				}))
				done := make(chan resp, 1)
				go func() {
					if operation == "rollback" {
						done <- a.api(t, "PUT", "/v1/sites/"+team+"/active-version?owner="+slug, map[string]int{"version_number": 1}, key)
					} else {
						done <- a.deployTeam(t, slug, team, key, "forbidden")
					}
				}()
				select {
				case <-reached:
				case <-time.After(10 * time.Second):
					close(resume)
					t.Fatal("write never reached gate")
				}
				_, err := a.database.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slug)
				close(resume)
				if err != nil {
					t.Fatal(err)
				}
				var result resp
				select {
				case result = <-done:
				case <-time.After(10 * time.Second):
					t.Fatal("write did not finish")
				}
				wantTS(t, "changed authority", result, 409, "submissions_closed")
				var active, versions int
				err = a.database.QueryRow(`SELECT COALESCE(max(s.active_version),0), count(v.id) FROM sites s JOIN events e ON e.account_id=s.user_id LEFT JOIN versions v ON v.site_id=s.id WHERE e.slug=$1 AND s.name=$2`, slug, team).Scan(&active, &versions)
				if err != nil {
					t.Fatal(err)
				}
				want := 2
				if operation == "create" {
					want = 0
				}
				if active != want || versions != want {
					t.Fatalf("refused write changed site: active=%d versions=%d", active, versions)
				}
			})
		}
	}
}

func TestHeldSiteSuspensionOperatorPrecedenceAndRollback(t *testing.T) {
	a := newTeamSiteApp(t)
	org, member := a.newPerson(t, "org"), a.newPerson(t, "member")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	team, _ := a.startTeam(t, slug, "Suspended team", member)
	key := member.key
	markReady(t, a.certDir, slug)
	wantTS(t, "deploy", a.deployTeam(t, slug, team, key, "v1"), 201, "")
	var siteID, accountID string
	if err := a.database.QueryRow(`SELECT s.id,s.user_id FROM sites s JOIN events e ON e.account_id=s.user_id WHERE e.slug=$1 AND s.name=$2`, slug, team).Scan(&siteID, &accountID); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	injected := errors.New("product update failed")
	err := a.sites.SuspendHeldSite(ctx, sites.HeldSiteSuspension{AccountID: accountID, Name: team, On: true, Prefix: "Taken down by the event organiser", Reason: "test", Also: func(tx *sql.Tx) error {
		var reason string
		if err := tx.QueryRow(`SELECT suspended_reason FROM sites WHERE id=$1`, siteID).Scan(&reason); err != nil {
			t.Fatal(err)
		}
		if reason != "Taken down by the event organiser: test" {
			t.Fatalf("product update preceded site update: %q", reason)
		}
		return injected
	}})
	if !errors.Is(err, injected) {
		t.Fatalf("callback error: %v", err)
	}
	site, err := siterepo.GetSiteByID(ctx, a.database, siteID)
	if err != nil || site.Suspended() {
		t.Fatalf("failed suspension persisted: %+v %v", site, err)
	}
	if err := siterepo.SetSiteSuspended(ctx, a.database, siteID, "operator reason"); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "organiser take-down", a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/"+team+"/takedown", map[string]string{"reason": "organiser reason"}, org.key), 200, "")
	wantTS(t, "organiser restore", a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/"+team+"/restore", nil, org.key), 409, "platform_takedown")
	var reason string
	if err := a.database.QueryRow(`SELECT suspended_reason FROM sites WHERE id=$1`, siteID).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != "operator reason" {
		t.Fatalf("operator reason changed: %q", reason)
	}
	var down bool
	if err := a.database.QueryRow(`SELECT t.site_taken_down_at IS NOT NULL FROM event_teams t JOIN events e ON e.id=t.event_id WHERE e.slug=$1 AND t.slug=$2`, slug, team).Scan(&down); err != nil {
		t.Fatal(err)
	}
	if !down {
		t.Fatal("refused organiser restore cleared product suspension")
	}
}
