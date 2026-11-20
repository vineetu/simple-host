package hackhttp

import (
	"database/sql"
	"errors"
	"log"
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/app/teams"
)

// teamSentinels are the team repository's refusals and their wire codes.
var teamSentinels = []struct {
	err       error
	code, msg string
}{
	{teams.ErrFindingStage, "teams_locked", "teams cannot be changed at this stage"},
	{teams.ErrFindingOff, "team_finding_off", "team finding is off"},
	{teams.ErrTeamFull, "team_full", "this team is full"},
	{teams.ErrTeamLocked, "team_locked", "this team isn't taking new people"},
	{teams.ErrTeamKept, "team_kept", "this team can't take new members"},
	{teams.ErrLastTeamAdmin, "last_team_admin", "make someone else admin first"},
	{teams.ErrTeamAdminOnly, "team_admin_only", "only the team's admins can do that"},
	{teams.ErrAlreadyInTeam, "already_in_team", "you are already on a team"},
	{teams.ErrTeamFrozen, "submissions_closed", "the team's deadline has passed"},
	{teams.ErrFindingForbidden, "team_action_unavailable", "this team action is not available"},
	{teams.ErrNotParticipant, "team_action_unavailable", "this team action is not available"},
	{teams.ErrTeamNotFound, "team_not_found", "team not found"},
}

// writeHackError is the one hack refusal writer. A workflow Error answers
// its code at httpx.RefusalStatus (409 where the table is silent), a team
// sentinel its code, a missing row event_not_found, anything else 500 (logged).
func writeHackError(w http.ResponseWriter, err error) {
	var e *events.Error
	if errors.As(err, &e) && e.Code != "internal" {
		httpx.WriteError(w, httpx.RefusalStatus(e.Code, http.StatusConflict), e.Code, e.Message)
		return
	}
	for _, s := range teamSentinels {
		if errors.Is(err, s.err) {
			httpx.WriteError(w, httpx.RefusalStatus(s.code, http.StatusConflict), s.code, s.msg)
			return
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	log.Printf("hack: %v", err)
	httpx.WriteInternal(w)
}
