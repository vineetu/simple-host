package hackevents

import (
	"context"
	"net/http"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/repository/sqlkit"
	siterules "github.com/vsriram/simple-host/host/rules/sites"
)

// GroupKeys supplies Hack's live team-key and scoped file-link checks.
type GroupKeys struct{}

func (GroupKeys) Binding(ctx context.Context, q sqlkit.Querier, keyHash string) (string, string, error) {
	return TeamKeyBinding(ctx, q, keyHash)
}

func (GroupKeys) Identity(ctx context.Context, q sqlkit.Querier, memberID, teamID string) (model.TeamIdentity, error) {
	return ResolveTeamIdentity(ctx, q, memberID, teamID)
}

func (GroupKeys) WriteRefusal(ctx context.Context, q sqlkit.Querier, teamID string) (status int, code, msg string, err error) {
	st, err := TeamWriteStateFor(ctx, q, teamID, false)
	if err != nil {
		return 0, "", "", err
	}
	code = st.WriteRefusal()
	if code != "" {
		status, msg = TeamRefusal(code)
	}
	return status, code, msg, nil
}

func (GroupKeys) FileLinkAllows(ctx context.Context, q sqlkit.Querier, site model.Site, kind, id, memberID string) bool {
	switch kind {
	case "team":
		team, err := ResolveTeamIdentity(ctx, q, memberID, id)
		return err == nil && team.AccountID == site.UserID && team.TeamSlug == site.Name
	case "event":
		ev, err := GetEventByAccount(ctx, q, site.UserID)
		if err != nil || ev.ID != id || site.Name != ev.ID || ev.TakenDown() {
			return false
		}
		member, err := GetEventMember(ctx, q, ev.ID, memberID)
		return err == nil && member.Role == "organiser"
	}
	return false
}

func (GroupKeys) Messages() (scopeOnly, inactive string) {
	return "this is a team key: it publishes and looks after your team's site only. " +
			"Use your own sign-in on the event platform for anything else.",
		"this team key stopped working: its person is no longer on that team. Get a new key from your event page on simple-hack.app."
}

// TeamRefusal is the status and message for a TeamWriteState refusal code.
func TeamRefusal(code string) (int, string) {
	status := http.StatusConflict
	if code == "event_taken_down" || code == "team_site_taken_down" {
		status = http.StatusForbidden
	}
	return status, siterules.TeamRefusalMessage(code)
}
