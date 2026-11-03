package db

import (
	"context"
	"database/sql"
	repo "github.com/vsriram/simple-host/internal/repository/hackentries"
)

type VotingSettings = repo.VotingSettings
type VoteOption = repo.VoteOption

var GetVotingSettings = repo.GetVotingSettings
var SaveVotingSettings = repo.SaveVotingSettings
var SetDirectoryListed = repo.SetDirectoryListed
var VoteOptions = repo.VoteOptions
var VoteFinalRanking = repo.VoteFinalRanking
var OwnVote = repo.OwnVote
var VoterEligible = repo.VoterEligible
var IneligibleVotes = repo.IneligibleVotes

type DirectoryEvent struct {
	Slug, Title, Tagline, Stage, TimeZone string
	AccentColor                           string
	StartsAt, EndsAt                      sql.NullTime
	// zp-hack-applications.sql: the organiser's switch and whether the
	// platform admin has taken the event down, for the applications_open
	// rule (always false here: the WHERE clause already excludes taken-down
	// events, but the row carries it so the rule is computed the one shared
	// way everywhere it is used).
	ApplicationsOpen bool
	TakenDownAt      sql.NullTime
}

func ListDirectoryEvents(ctx context.Context, q Querier) ([]DirectoryEvent, error) {
	rows, err := queryContext(ctx, q, `SELECT slug, title, tagline, stage, time_zone, accent_color, starts_at, ends_at, applications_open, taken_down_at
		FROM events WHERE directory_listed AND stage <> 'draft' AND taken_down_at IS NULL
		ORDER BY starts_at NULLS LAST, slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DirectoryEvent{}
	for rows.Next() {
		var e DirectoryEvent
		if err := rows.Scan(&e.Slug, &e.Title, &e.Tagline, &e.Stage, &e.TimeZone, &e.AccentColor, &e.StartsAt, &e.EndsAt, &e.ApplicationsOpen, &e.TakenDownAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
