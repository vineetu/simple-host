package events

import (
	"database/sql"
	"testing"
	"time"

	"github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
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
		{"admin gets join request", mailMember{EventMember: model.EventMember{Role: "participant", TeamID: team, TeamAdmin: true}, stage: "open"}, notice(hackmembers.NoticeJoinRequest, "t1", ""), true},
		{"non-admin does not", mailMember{EventMember: model.EventMember{Role: "participant", TeamID: team}, stage: "open"}, notice(hackmembers.NoticeJoinRequest, "t1", ""), false},
		{"teams no longer forming", mailMember{EventMember: model.EventMember{Role: "participant", TeamID: team, TeamAdmin: true}, stage: "judging"}, notice(hackmembers.NoticeJoinRequest, "t1", ""), false},
		{"teamless participant gets invite", mailMember{EventMember: model.EventMember{Role: "participant"}, stage: "building"}, notice(hackmembers.NoticeInvite, "", ""), true},
		{"invite after joining a team", mailMember{EventMember: model.EventMember{Role: "participant", TeamID: team}, stage: "building"}, notice(hackmembers.NoticeInvite, "", ""), false},
		{"organiser gets applications", mailMember{EventMember: model.EventMember{Role: "organiser"}, stage: "open"}, notice(hackmembers.NoticeApplicationNew, "", ""), true},
		{"not once archived", mailMember{EventMember: model.EventMember{Role: "organiser"}, stage: "archived"}, notice(hackmembers.NoticeTeamInbound, "", ""), false},
		{"decision still current", mailMember{EventMember: model.EventMember{Role: "participant", ApprovalStatus: "approved"}}, notice(hackmembers.NoticeApplicationDecided, "", "approved"), true},
		{"decision since changed", mailMember{EventMember: model.EventMember{Role: "participant", ApprovalStatus: "pending"}}, notice(hackmembers.NoticeApplicationDecided, "", "approved"), false},
		{"judge news has no member rule", mailMember{EventMember: model.EventMember{Role: "judge"}}, notice(hackmembers.NoticeJudgingOpen, "", ""), true},
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
