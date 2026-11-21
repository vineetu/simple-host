package hackteams

import "testing"

func TestRequestDecision(t *testing.T) {
	for _, tc := range []struct {
		name        string
		f           requestFacts
		actor, kind string
		answer      string
		err         error
	}{
		{"own request", requestFacts{}, "p", "request", "", nil},
		{"request for someone else", requestFacts{}, "x", "request", "", ErrHackFindingForbidden},
		{"invite by a stranger", requestFacts{looking: true}, "x", "invite", "", ErrHackFindingForbidden},
		{"invite to someone not looking", requestFacts{teammate: true}, "x", "invite", "", ErrHackFindingForbidden},
		{"invite answering their request", requestFacts{teammate: true, existingID: "r", existingKind: "request"}, "x", "invite", "", nil},
		{"same request again", requestFacts{existingID: "r", existingKind: "request"}, "p", "request", "pending", nil},
	} {
		answer, err := requestDecision(tc.f, tc.actor, "p", tc.kind)
		if answer != tc.answer || err != tc.err {
			t.Errorf("%s: got %q, %v", tc.name, answer, err)
		}
	}
}

func TestMayResolve(t *testing.T) {
	for _, tc := range []struct {
		action, kind, actor string
		teammate, admin     bool
		want                error
	}{
		{"withdraw", "request", "p", false, false, nil},
		{"withdraw", "request", "x", true, true, ErrHackFindingForbidden},
		{"withdraw", "invite", "x", true, false, nil},
		{"withdraw", "invite", "p", false, false, ErrHackFindingForbidden},
		{"accept", "request", "x", true, true, nil},
		{"decline", "request", "x", true, false, ErrHackTeamAdminOnly},
		{"accept", "invite", "p", false, false, nil},
		{"decline", "invite", "x", true, true, ErrHackFindingForbidden},
	} {
		if got := mayResolve(tc.action, tc.kind, tc.actor, "p", tc.teammate, tc.admin); got != tc.want {
			t.Errorf("%+v: got %v", tc, got)
		}
	}
}
