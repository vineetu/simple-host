package hackevents

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
)

type SignupQuestion = model.SignupQuestion

type SignupAnswer = model.SignupAnswer

type EventTrack = model.EventTrack

// RegistrationSettings loads the registration questions and approval setting.
func RegistrationSettings(ctx context.Context, q sqlkit.Querier, eventID string) ([]SignupQuestion, bool, error) {
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

func SetRegistrationSettings(ctx context.Context, q sqlkit.Querier, eventID string, questions []SignupQuestion, approval bool) error {
	raw, err := json.Marshal(questions)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `UPDATE events SET signup_questions=$2,approval_required=$3,updated_at=now() WHERE id=$1`, eventID, raw, approval)
	return err
}

func ListEventTracks(ctx context.Context, q sqlkit.Querier, eventID string) ([]EventTrack, error) {
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

func ReplaceEventTracks(ctx context.Context, q sqlkit.Querier, eventID string, tracks []EventTrack) error {
	// No position column (plan: no schema change).
	base := time.Now().UTC()
	for i, t := range tracks {
		createdAt := base.Add(time.Duration(i) * time.Microsecond)
		if _, err := q.ExecContext(ctx, `INSERT INTO event_tracks(event_id,slug,name,challenge,prize,created_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(event_id,slug) DO UPDATE SET name=EXCLUDED.name,challenge=EXCLUDED.challenge,prize=EXCLUDED.prize,created_at=EXCLUDED.created_at`, eventID, t.Slug, t.Name, t.Challenge, t.Prize, createdAt); err != nil {
			return err
		}
	}
	slugs := make([]string, 0, len(tracks))
	for _, t := range tracks {
		slugs = append(slugs, t.Slug)
	}
	// No position column.
	_, err := q.ExecContext(ctx, `DELETE FROM event_tracks WHERE event_id=$1 AND NOT(slug=ANY($2))`, eventID, pq.Array(slugs))
	return err
}

// TrackIDBySlug is the id of the event's track named slug.
func TrackIDBySlug(ctx context.Context, q sqlkit.Querier, eventID, slug string) (string, error) {
	var id string
	err := q.QueryRowContext(ctx, `SELECT id FROM event_tracks WHERE event_id=$1 AND slug=$2`, eventID, slug).Scan(&id)
	return id, err
}

// TracksByIDs loads these tracks by id; an id with no track is absent.
func TracksByIDs(ctx context.Context, q sqlkit.Querier, ids []string) (map[string]EventTrack, error) {
	out := map[string]EventTrack{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := queryContext(ctx, q, `SELECT id,slug,name,challenge,prize FROM event_tracks WHERE id = ANY($1::uuid[])`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t EventTrack
		if err := rows.Scan(&t.ID, &t.Slug, &t.Name, &t.Challenge, &t.Prize); err != nil {
			return nil, err
		}
		out[t.ID] = t
	}
	return out, rows.Err()
}
