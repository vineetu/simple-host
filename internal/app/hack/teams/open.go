package teams

// TODO(4.2): move inline SQL to repository/hackteams.
import (
	"context"
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/db"
)

// OpenTeam is a team shown in the finding directory.
type OpenTeam struct {
	Slug, Name, LookingFor string
	Members                []string
	Track                  *db.EventTrack
}

// SoloPerson is an opted-in participant without a team.
type SoloPerson struct{ UserID, DisplayName, Intro string }

// FindingTeamState describes the caller's team.
type FindingTeamState struct {
	LookingFor    string
	Locked, Admin bool
}

// FindingDirectory contains the results of the finding reads.
type FindingDirectory struct {
	Teams   []OpenTeam
	Solo    []SoloPerson
	Pending []db.TeamRequest
	Looking bool
	Intro   string
	Team    *FindingTeamState
}

// ListOpenTeams reads the finding directory visible to a member.
func (s *Service) ListOpenTeams(ctx context.Context, eventID, userID string, member db.EventMember) (FindingDirectory, error) {
	out := FindingDirectory{Teams: []OpenTeam{}, Solo: []SoloPerson{}, Pending: []db.TeamRequest{}}
	if !member.TeamID.Valid || member.Role == "organiser" {
		rows, err := s.db.QueryContext(ctx, `SELECT t.id,t.slug,t.name,t.looking_for,
 COALESCE(x.id::text,''),COALESCE(x.slug,''),COALESCE(x.name,''),COALESCE(x.challenge,''),COALESCE(x.prize,'')
 FROM event_teams t JOIN events e ON e.id=t.event_id LEFT JOIN event_tracks x ON x.id=t.track_id
 WHERE t.event_id=$1 AND t.locked_at IS NULL
   AND e.stage NOT IN ('judging','results','archived')
   AND NOT COALESCE(`+db.EffectiveDeadlineSQL+` <= clock_timestamp(), false)
 ORDER BY t.created_at`, eventID)
		if err != nil {
			return out, err
		}
		byID := map[string]int{}
		for rows.Next() {
			var id string
			var t OpenTeam
			var track db.EventTrack
			if err = rows.Scan(&id, &t.Slug, &t.Name, &t.LookingFor, &track.ID, &track.Slug, &track.Name, &track.Challenge, &track.Prize); err != nil {
				rows.Close()
				return out, err
			}
			if track.ID != "" {
				t.Track = &track
			}
			t.Members = []string{}
			byID[id] = len(out.Teams)
			out.Teams = append(out.Teams, t)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
		rows, err = s.db.QueryContext(ctx, `SELECT m.team_id,m.display_name FROM event_members m JOIN event_teams t ON t.id=m.team_id WHERE t.event_id=$1 AND t.locked_at IS NULL AND m.approval_status='approved' ORDER BY m.joined_at,m.user_id`, eventID)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var id, name string
			if err = rows.Scan(&id, &name); err != nil {
				rows.Close()
				return out, err
			}
			if i, ok := byID[id]; ok {
				out.Teams[i].Members = append(out.Teams[i].Members, name)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	if member.TeamID.Valid || member.Role == "organiser" {
		rows, err := s.db.QueryContext(ctx, `SELECT user_id,display_name,looking_intro FROM event_members WHERE event_id=$1 AND role='participant' AND approval_status='approved' AND team_id IS NULL AND looking ORDER BY joined_at,user_id`, eventID)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var p SoloPerson
			if err = rows.Scan(&p.UserID, &p.DisplayName, &p.Intro); err != nil {
				rows.Close()
				return out, err
			}
			out.Solo = append(out.Solo, p)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.id,r.team_id,t.slug,t.name,r.user_id,m.display_name,r.kind,r.created_at FROM event_team_requests r JOIN event_teams t ON t.id=r.team_id JOIN event_members m ON m.event_id=r.event_id AND m.user_id=r.user_id WHERE r.event_id=$1 AND (r.user_id=$2 OR r.team_id=$3 OR $4) ORDER BY r.created_at`, eventID, userID, member.TeamID, member.Role == "organiser")
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var p db.TeamRequest
		if err = rows.Scan(&p.ID, &p.TeamID, &p.Team, &p.TeamName, &p.UserID, &p.DisplayName, &p.Kind, &p.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Pending = append(out.Pending, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if err = s.db.QueryRowContext(ctx, `SELECT looking,looking_intro FROM event_members WHERE event_id=$1 AND user_id=$2`, eventID, userID).Scan(&out.Looking, &out.Intro); err != nil {
		return out, err
	}
	if member.TeamID.Valid {
		var t FindingTeamState
		if err = s.db.QueryRowContext(ctx, `
			SELECT t.locked_at IS NOT NULL, t.looking_for, m.team_admin
			  FROM event_teams t JOIN event_members m ON m.team_id = t.id
			 WHERE t.id = $1 AND m.event_id = $2 AND m.user_id = $3`, member.TeamID, eventID, userID).Scan(&t.Locked, &t.LookingFor, &t.Admin); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		out.Team = &t
	}
	return out, nil
}
