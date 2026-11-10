package events

import (
	"github.com/vsriram/simple-host/internal/model"
	"testing"
	"time"
)

func TestMembershipAbilities(t *testing.T) {
	for _, tc := range []struct {
		name, role string
		member     bool
		want       Abilities
	}{
		{"organiser", "organiser", true, Abilities{Manage: true}},
		{"judge", "judge", true, Abilities{Judge: true}},
		{"participant approved", "participant", true, Abilities{TakePart: true}},
		{"participant pending", "participant", true, Abilities{TakePart: true}},
		{"participant rejected", "participant", true, Abilities{TakePart: true}},
		{"synthetic admin_view", "organiser", false, Abilities{}},
		{"member admin", "participant", true, Abilities{TakePart: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := model.EventMember{Role: tc.role}
			if tc.member {
				m.JoinedAt = time.Now()
			}
			if got := MembershipAbilities(m); got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestChecklistJudgeReadyDetailPlurals(t *testing.T) {
	for _, tc := range []struct {
		judges, organisers int
		want               string
	}{
		{1, 1, "1 judge joined"}, {2, 1, "2 judges joined"},
		{0, 1, "0 judges joined · 1 organiser can judge"},
		{0, 2, "0 judges joined · 2 organisers can judge"},
		{0, 0, "0 judges joined"},
	} {
		if got := judgeReadyDetail(tc.judges, tc.organisers); got != tc.want {
			t.Fatalf("%d/%d: %s", tc.judges, tc.organisers, got)
		}
	}
}
