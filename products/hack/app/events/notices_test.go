package events

import (
	"database/sql"
	"testing"
	"time"

	"github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

func TestMailMemberReceives(t *testing.T) {
	team := sql.NullString{String: "t1", Valid: true}
	notice := func(kind, teamID, detail string) model.MailNotice {
		var n model.MailNotice
		n.Kind, n.TeamID, n.Detail = kind, teamID, detail
		return n
	}
	for _, tc := range []struct {
		name string
		m    mailMember
		n    model.MailNotice
		want bool
	}{
		{"admin gets join request", mailMember{role: "participant", stage: "open", teamID: team, admin: true}, notice(hackevents.NoticeJoinRequest, "t1", ""), true},
		{"non-admin does not", mailMember{role: "participant", stage: "open", teamID: team}, notice(hackevents.NoticeJoinRequest, "t1", ""), false},
		{"teams no longer forming", mailMember{role: "participant", stage: "judging", teamID: team, admin: true}, notice(hackevents.NoticeJoinRequest, "t1", ""), false},
		{"teamless participant gets invite", mailMember{role: "participant", stage: "building"}, notice(hackevents.NoticeInvite, "", ""), true},
		{"invite after joining a team", mailMember{role: "participant", stage: "building", teamID: team}, notice(hackevents.NoticeInvite, "", ""), false},
		{"organiser gets applications", mailMember{role: "organiser", stage: "open"}, notice(hackevents.NoticeApplicationNew, "", ""), true},
		{"not once archived", mailMember{role: "organiser", stage: "archived"}, notice(hackevents.NoticeTeamInbound, "", ""), false},
		{"decision still current", mailMember{role: "participant", status: "approved"}, notice(hackevents.NoticeApplicationDecided, "", "approved"), true},
		{"decision since changed", mailMember{role: "participant", status: "pending"}, notice(hackevents.NoticeApplicationDecided, "", "approved"), false},
		{"judge news has no member rule", mailMember{role: "judge"}, notice(hackevents.NoticeJudgingOpen, "", ""), true},
	} {
		if got := tc.m.receives(tc.n); got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
}

func TestRateLimit(t *testing.T) {
	old := sql.NullTime{Valid: true, Time: time.Now().Add(-59*time.Minute - 59*time.Second)}
	for _, tc := range []struct {
		used  int
		old   sql.NullTime
		retry int
	}{
		{50, old, 0},               // at the limit is allowed
		{51, sql.NullTime{}, 3601}, // no oldest row: wait the whole window
		{51, old, 2},               // floor of one second, plus one
	} {
		err := rateLimit(tc.used, 50, tc.old, time.Hour)
		got := 0
		if rl, ok := err.(*RateLimitError); ok {
			got = rl.RetryAfter
		}
		if got != tc.retry {
			t.Errorf("used %d: retry %d want %d", tc.used, got, tc.retry)
		}
	}
}
