package hackevents

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
)

type VotingSettings = model.VotingSettings

// GetVotingSettings reads the event's public voting settings.
func GetVotingSettings(ctx context.Context, q sqlkit.Querier, eventID string) (VotingSettings, error) {
	var s VotingSettings
	err := q.QueryRowContext(ctx, `SELECT voting_enabled, voting_opens_at, voting_closes_at,
		voting_eligibility, directory_listed FROM events WHERE id = $1`, eventID).
		Scan(&s.Enabled, &s.OpensAt, &s.ClosesAt, &s.Eligibility, &s.DirectoryListed)
	return s, err
}

// SaveVotingSettings writes the event's public voting settings.
func SaveVotingSettings(ctx context.Context, q sqlkit.Querier, eventID string, s VotingSettings) error {
	_, err := q.ExecContext(ctx, `UPDATE events SET voting_enabled = $2, voting_opens_at = $3,
		voting_closes_at = $4, voting_eligibility = $5, directory_listed = $6,
		updated_at = now() WHERE id = $1`, eventID, s.Enabled, nullTime(s.OpensAt),
		nullTime(s.ClosesAt), s.Eligibility, s.DirectoryListed)
	return err
}

// SetDirectoryListed lists or unlists the event in the public directory.
func SetDirectoryListed(ctx context.Context, q sqlkit.Querier, eventID string, listed bool) error {
	_, err := q.ExecContext(ctx, `UPDATE events SET directory_listed = $2, updated_at = now() WHERE id = $1`, eventID, listed)
	return err
}

// VoteWindow is the event's voting state at the moment a vote is cast.
type VoteWindow struct {
	Enabled, GalleryOpen bool
	Opens, Closes        sql.NullTime
	Eligibility, Stage   string
	TakenDown            sql.NullTime
}

// LockVoteWindow locks the event FOR SHARE and reads its voting state.
func LockVoteWindow(ctx context.Context, tx *sql.Tx, eventID string) (VoteWindow, error) {
	var v VoteWindow
	err := tx.QueryRowContext(ctx, `SELECT voting_enabled, gallery_open, voting_opens_at, voting_closes_at,
		voting_eligibility, stage, taken_down_at FROM events WHERE id = $1 FOR SHARE`, eventID).
		Scan(&v.Enabled, &v.GalleryOpen, &v.Opens, &v.Closes, &v.Eligibility, &v.Stage, &v.TakenDown)
	return v, err
}
