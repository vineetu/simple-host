package servertest

import (
	"context"
	"errors"
	"reflect"
	"testing"

	appstorage "github.com/vsriram/simple-host/host/data"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
	"github.com/vsriram/simple-host/products/hack/app/storagebind"
)

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

// bindingEvent is an event with a team site, a second team, and the event
// website, made through the front door.
type bindingEvent struct {
	a                                        *teamSiteApp
	slug, team, other, eventID               string
	teamSite, website                        string
	member, otherMember, organiser, outsider string
	memberKey                                string
}

func newBindingEvent(t *testing.T, a *teamSiteApp) bindingEvent {
	t.Helper()
	org, member, other, outsider := a.newPerson(t, "binding-org"), a.newPerson(t, "binding-member"), a.newPerson(t, "binding-other"), a.newPerson(t, "binding-outsider")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	a.join(t, slug, other, org)
	team, _ := a.startTeam(t, slug, "Bindings", member)
	otherTeam, _ := a.startTeam(t, slug, "Others", other)
	if a.certDir != "" {
		markReady(t, a.certDir, slug)
	}
	wantTS(t, "team site", a.deployTeam(t, slug, team, member.key, "team"), 201, "")
	var eid string
	if err := a.database.QueryRow(`SELECT id FROM events WHERE slug=$1`, slug).Scan(&eid); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "event site", a.deployTeam(t, slug, eid, org.key, "event"), 201, "")
	return bindingEvent{a, slug, team, otherTeam, eid, hackSiteID(t, a, slug, team), hackSiteID(t, a, slug, eid), a.uid(t, member), a.uid(t, other), a.uid(t, org), a.uid(t, outsider), member.key}
}

func TestHackStorageBindingValues(t *testing.T) {
	a := newTeamSiteApp(t)
	b := newBindingEvent(t, a)
	db := a.database
	p := storagebind.Provider{DB: db}
	if _, err := appstorage.MergeBindingSchema(false, p); err != nil {
		t.Fatal(err)
	}
	team, other, site, web := b.team, b.other, b.teamSite, b.website
	member := b.member
	needed := []string{"auth.team", "auth.teams", "auth.organiser", "event.team", "event.stage", "event.before_deadline"}
	for _, tc := range []struct {
		name, site, user, change string
		want                     ruleengine.Values
	}{
		{"teammate before", site, member, "", bindingValues(team, team, "open", false, true)},
		{"teammate after", site, member, `UPDATE events SET submission_deadline=now()-interval '1 day'`, bindingValues(team, team, "open", false, false)},
		{"extended before", site, member, `UPDATE events SET submission_deadline=now()-interval '1 day'; UPDATE event_teams SET deadline_override=now()+interval '1 day'`, bindingValues(team, team, "open", false, true)},
		{"extended after", site, member, `UPDATE events SET submission_deadline=now()-interval '2 days'; UPDATE event_teams SET deadline_override=now()-interval '1 day'`, bindingValues(team, team, "open", false, false)},
		{"override cannot shorten", site, member, `UPDATE event_teams SET deadline_override=now()-interval '1 day'`, bindingValues(team, team, "open", false, true)},
		{"no deadline", site, member, `UPDATE events SET submission_deadline=NULL`, bindingValues(team, team, "open", false, true)},
		{"override without event deadline", site, member, `UPDATE events SET submission_deadline=NULL; UPDATE event_teams SET deadline_override=now()-interval '1 day'`, bindingValues(team, team, "open", false, true)},
		{"pending", site, member, `UPDATE event_members SET approval_status='pending' WHERE role='participant'`, bindingValues("", team, "open", false, true)},
		{"organiser website", web, b.organiser, "", bindingValues("", "", "open", true, true)},
		{"organiser archived", web, b.organiser, `UPDATE events SET stage='archived'`, bindingValues("", "", "archived", true, true)},
		{"website deadline has no team override", web, b.organiser, `UPDATE events SET submission_deadline=now()-interval '1 day'; UPDATE event_teams SET deadline_override=now()+interval '1 day'`, bindingValues("", "", "open", true, false)},
		{"other team", site, b.otherMember, "", bindingValues(other, team, "open", false, true)},
		{"judge", site, member, `UPDATE event_members SET role='judge',team_id=NULL WHERE role='participant'`, bindingValues("", team, "open", false, true)},
		{"anonymous", site, "", "", bindingValues("", team, "open", false, true)},
		{"not a member", site, b.outsider, "", bindingValues("", team, "open", false, true)},
		{"no event", "00000000-0000-0000-0000-000000000000", member, "", bindingValues("", "", "", false, false)},
		{"take downs remain gates", site, member, `UPDATE events SET taken_down_at=now(); UPDATE event_teams SET site_taken_down_at=now(); UPDATE sites SET suspended_at=now()`, bindingValues(team, team, "open", false, true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if tc.change != "" {
				if _, err := tx.Exec(tc.change); err != nil {
					t.Fatal(err)
				}
			}
			// Only the stored site determines the event, not the other call fields.
			got, err := (storagebind.Provider{DB: tx}).Values(context.Background(), appstorage.BindingCall{SiteID: tc.site, UserID: tc.user, OwnerID: "ignored", SiteName: "ignored"}, needed)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("values = %#v, %v; want %#v", got, err, tc.want)
			}
		})
	}
	got, err := p.Values(context.Background(), appstorage.BindingCall{SiteID: site}, []string{"event.stage"})
	if err != nil || !reflect.DeepEqual(got, ruleengine.Values{"event.stage": ruleengine.Text("open")}) {
		t.Fatalf("needed subset: %#v, %v", got, err)
	}
}

func TestStorageOwnerOnSiteEdgeCases(t *testing.T) {
	a := newTeamSiteApp(t)
	b := newBindingEvent(t, a)
	eid, team, teamSite, eventSite := b.eventID, b.team, b.teamSite, b.website
	mid, oid, otherID := b.member, b.organiser, b.outsider
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
			got, err := (storagebind.Provider{DB: tx}).OwnerOnSite(context.Background(), tc.site, tc.user)
			if err != nil || got != tc.want {
				t.Fatalf("provider=%v/%v want=%v", got, err, tc.want)
			}
		})
	}
	t.Run("database error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, err := (storagebind.Provider{DB: a.database}).OwnerOnSite(ctx, teamSite, mid)
		if got || !errors.Is(err, context.Canceled) {
			t.Fatalf("provider=%v/%v", got, err)
		}
	})
}
