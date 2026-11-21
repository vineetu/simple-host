package events

import (
	"testing"
	"time"

	"github.com/vsriram/simple-host/products/hack/model"
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
