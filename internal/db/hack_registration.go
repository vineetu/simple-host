package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/lib/pq"
	"time"
)

type SignupQuestion struct {
	ID       string `json:"id"`
	Prompt   string `json:"prompt"`
	Required bool   `json:"required"`
}
type SignupAnswer struct {
	ID     string `json:"id"`
	Prompt string `json:"prompt"`
	Answer string `json:"answer"`
}
type EventTrack struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Challenge string `json:"challenge"`
	Prize     string `json:"prize"`
}
type RegistrationApplication struct {
	UserID      string         `json:"user_id"`
	Email       string         `json:"email"`
	DisplayName string         `json:"display_name"`
	Status      string         `json:"status"`
	Answers     []SignupAnswer `json:"answers"`
	JoinedAt    time.Time      `json:"joined_at"`
}

func RegistrationSettings(ctx context.Context, q Querier, eventID string) ([]SignupQuestion, bool, error) {
	var raw []byte
	var approval bool
	err := q.QueryRowContext(ctx, `SELECT signup_questions,approval_required FROM events WHERE id=$1`, eventID).Scan(&raw, &approval)
	if err != nil {
		return nil, false, err
	}
	var questions []SignupQuestion
	if err = json.Unmarshal(raw, &questions); err != nil {
		return nil, false, err
	}
	if questions == nil {
		questions = []SignupQuestion{}
	}
	return questions, approval, nil
}
func SetRegistrationSettings(ctx context.Context, q Querier, eventID string, questions []SignupQuestion, approval bool) error {
	raw, err := json.Marshal(questions)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `UPDATE events SET signup_questions=$2,approval_required=$3,updated_at=now() WHERE id=$1`, eventID, raw, approval)
	return err
}
func MemberApprovalStatus(ctx context.Context, q Querier, eventID, userID string) (string, error) {
	var status string
	err := q.QueryRowContext(ctx, `SELECT approval_status FROM event_members WHERE event_id=$1 AND user_id=$2`, eventID, userID).Scan(&status)
	return status, err
}
func JoinParticipantRegistration(ctx context.Context, q Querier, eventID, userID, display string, answers []SignupAnswer, pending bool) (EventMember, error) {
	raw, err := json.Marshal(answers)
	if err != nil {
		return EventMember{}, err
	}
	status := "approved"
	if pending {
		status = "pending"
	}
	return scanMember(q.QueryRowContext(ctx, `INSERT INTO event_members (event_id,user_id,role,display_name,coc_accepted_at,signup_answers,approval_status) VALUES ($1,$2,'participant',$3,now(),$4,$5) RETURNING event_id,user_id,role,display_name,team_id,coc_accepted_at,joined_at`, eventID, userID, display, raw, status))
}
func ListRegistrationApplications(ctx context.Context, q Querier, eventID string) ([]RegistrationApplication, error) {
	rows, err := queryContext(ctx, q, `SELECT m.user_id,u.username,m.display_name,m.approval_status,m.signup_answers,m.joined_at FROM event_members m JOIN users u ON u.id=m.user_id WHERE m.event_id=$1 AND m.role='participant' ORDER BY m.joined_at`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RegistrationApplication{}
	for rows.Next() {
		var a RegistrationApplication
		var raw []byte
		if err := rows.Scan(&a.UserID, &a.Email, &a.DisplayName, &a.Status, &raw, &a.JoinedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &a.Answers); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func DecideRegistration(ctx context.Context, q Querier, eventID, userID, actor, status string) (bool, error) {
	result, err := q.ExecContext(ctx, `UPDATE event_members SET approval_status=$4,approval_decided_at=now(),approval_decided_by=$3 WHERE event_id=$1 AND user_id=$2 AND role='participant' AND approval_status='pending'`, eventID, userID, actor, status)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
func ListEventTracks(ctx context.Context, q Querier, eventID string) ([]EventTrack, error) {
	rows, err := queryContext(ctx, q, `SELECT id,slug,name,challenge,prize FROM event_tracks WHERE event_id=$1 ORDER BY created_at,id`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EventTrack{}
	for rows.Next() {
		var t EventTrack
		if err := rows.Scan(&t.ID, &t.Slug, &t.Name, &t.Challenge, &t.Prize); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func ReplaceEventTracks(ctx context.Context, q Querier, eventID string, tracks []EventTrack) error {
	for _, t := range tracks {
		if _, err := q.ExecContext(ctx, `INSERT INTO event_tracks(event_id,slug,name,challenge,prize) VALUES($1,$2,$3,$4,$5) ON CONFLICT(event_id,slug) DO UPDATE SET name=EXCLUDED.name,challenge=EXCLUDED.challenge,prize=EXCLUDED.prize`, eventID, t.Slug, t.Name, t.Challenge, t.Prize); err != nil {
			return err
		}
	}
	slugs := make([]string, 0, len(tracks))
	for _, t := range tracks {
		slugs = append(slugs, t.Slug)
	}
	// Deletion of an unlisted track clears its teams' selection through the FK.
	_, err := q.ExecContext(ctx, `DELETE FROM event_tracks WHERE event_id=$1 AND NOT(slug=ANY($2))`, eventID, pq.Array(slugs))
	return err
}
func SetTeamTrack(ctx context.Context, q Querier, eventID, teamID, slug string) (string, error) {
	var id sql.NullString
	if slug != "" {
		if err := q.QueryRowContext(ctx, `SELECT id FROM event_tracks WHERE event_id=$1 AND slug=$2`, eventID, slug).Scan(&id); err != nil {
			return "", err
		}
	}
	result, err := q.ExecContext(ctx, `UPDATE event_teams SET track_id=$3 WHERE event_id=$1 AND id=$2`, eventID, teamID, id)
	if err != nil {
		return "", err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", sql.ErrNoRows
	}
	return id.String, nil
}
func TeamTrack(ctx context.Context, q Querier, teamID string) (*EventTrack, error) {
	var t EventTrack
	err := q.QueryRowContext(ctx, `SELECT x.id,x.slug,x.name,x.challenge,x.prize FROM event_teams t JOIN event_tracks x ON x.id=t.track_id WHERE t.id=$1`, teamID).Scan(&t.ID, &t.Slug, &t.Name, &t.Challenge, &t.Prize)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}
