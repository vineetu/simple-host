package servertest

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/vsriram/simple-host/host/sites"
	siterepo "github.com/vsriram/simple-host/host/sites/repo"
)

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
