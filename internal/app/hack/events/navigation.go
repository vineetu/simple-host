package events

import (
	"context"
	"encoding/json"

	"github.com/vsriram/simple-host/internal/model"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
	"github.com/vsriram/simple-host/internal/rules/hackevents"
)

type Abilities struct {
	Manage   bool `json:"manage"`
	Judge    bool `json:"judge"`
	TakePart bool `json:"take_part"`
}

// MembershipAbilities uses only actual membership, including for platform admins.
func MembershipAbilities(member model.EventMember) Abilities {
	if member.JoinedAt.IsZero() {
		return Abilities{}
	}
	return AbilitiesForRole(member.Role)
}

// AbilitiesForRole projects a role from the membership-only event listing.
func AbilitiesForRole(role string) Abilities {
	return Abilities{Manage: role == "organiser", Judge: role == "judge", TakePart: role == "participant"}
}

func Navigation() json.RawMessage { return hackevents.NavigationJSON() }

// NavigationHelp is the explanation layer of the navigation map (organiser
// assistant v2), typed.
func NavigationHelp() hackevents.NavigationHelpMap { return hackevents.NavigationHelp() }

// MemberAbilities projects actual membership with the canonical current judge set.
func (s *Service) MemberAbilities(ctx context.Context, eventID string, member model.EventMember) Abilities {
	can := MembershipAbilities(member)
	if member.JoinedAt.IsZero() {
		return can
	}
	can.Judge, _ = entryrepo.EventJudgeInEvent(ctx, s.db, eventID, member.UserID)
	return can
}
