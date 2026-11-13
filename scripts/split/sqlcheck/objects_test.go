package main

import (
	"strings"
	"testing"
)

func TestSplitSQL(t *testing.T) {
	s := `-- comment ;
CREATE FUNCTION f() RETURNS void AS $tag$ BEGIN PERFORM ';'; END; $tag$ LANGUAGE plpgsql;
/* ; /* nested ; */ ; */ SELECT 'it''s;quoted', "semi;colon"; SELECT $$body;$$;
SELECT 'last'; -- trailing ;
`
	got := splitSQL(s)
	if len(got) != 4 {
		t.Fatalf("%#v", got)
	}
	if got[0].Line != 2 || !strings.Contains(got[0].Text, "END;") {
		t.Fatal(got)
	}
	if len(splitSQL("-- only comment")) != 0 {
		t.Fatal("comment became statement")
	}
}
func TestOccurrences(t *testing.T) {
	for _, tt := range []struct {
		s    string
		want int
	}{{"events", 1}, {"site_events", 0}, {"events_foo", 0}, {"n.events", 0}, {`n."events"`, 0}, {`public."events"`, 1}, {"public.events", 1}, {"otherpublic.events", 0}, {"events.slug", 1}, {"Events", 1}, {"FROM eVeNtS", 1}, {"FROM EVENTS", 1}, {`FROM "EVENTS"`, 0}, {`FROM "Events"`, 0}, {`FROM "public"."events"`, 1}, {`FROM "PUBLIC".EVENTS`, 0}, {`FROM PUBLIC.Events`, 1}, {"PuBlIc.events", 1}, {"/events", 0}, {"events/", 0}, {"pre-events", 0}, {"events-slug", 0}, {`"events"`, 1}, {"events events", 2}, {"SELECT * FROM events-- note", 1}, {"FROM events/* note */", 1}, {"/* x */events", 1}, {"-events", 0}, {"events-", 0}, {"events/x", 0}, {`"events"-- note`, 1}, {`/* x */"events"`, 1}} {
		if n := len(occurrences(tt.s, "events")); n != tt.want {
			t.Errorf("%q: %d want %d", tt.s, n, tt.want)
		}
	}
}
func TestObjectProposal(t *testing.T) {
	_, _, z := fixture(t)
	o, info := proposeObjects(z)
	m := map[string]bool{}
	for _, x := range o {
		m[x.Name] = true
	}
	for _, n := range []string{"rubric_criteria", "derived", "events", "event_teams", "hack_members_notifications", "revoke_hack_notifications"} {
		if !m[n] {
			t.Error("missing", n)
		}
	}
	for _, n := range []string{"undecided", "event_domains", "site_notifications", "platform_trigger"} {
		if m[n] {
			t.Error("unexpected", n)
		}
	}
	s := strings.Join(info, "\n")
	for _, v := range []string{"foreign-key derivation", "references Hack and platform", "Hack function names platform table", "platform trigger naming Hack"} {
		if !strings.Contains(s, v) {
			t.Error("missing", v, s)
		}
	}
}
func TestTablePosition(t *testing.T) {
	for _, s := range []string{"FROM events", "FROM ONLY events", "CREATE TABLE IF NOT EXISTS events", "FROM sites, events", "events.slug", "JOIN public.events"} {
		i := strings.Index(s, "events")
		if !tablePosition(s, i, "events") {
			t.Error(s)
		}
	}
	for _, s := range []string{"SET events=$1", "SELECT events FROM sites", "SELECT x, events FROM sites"} {
		if tablePosition(s, strings.Index(s, "events"), "events") {
			t.Error(s)
		}
	}
}

func TestQualifiedOwnedColumn(t *testing.T) {
	for _, tt := range []struct {
		s    string
		want int
	}{{"SELECT g.selected_team_id FROM oauth_grants g", 1}, {"SELECT x.selected_team_id", 1}, {"SELECT oauth_grants.selected_team_id", 1}, {"SELECT g.selected_team_id_extra FROM oauth_grants g", 0}, {`SELECT g."selected_team_id" FROM oauth_grants AS g`, 1}} {
		if got := len(occurrences(tt.s, "selected_team_id", true)); got != tt.want {
			t.Errorf("%q: %d", tt.s, got)
		}
	}
}
