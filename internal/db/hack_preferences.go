package db

import (
	"context"
	"database/sql"
)

type HackPreferences struct {
	Theme                    string `json:"theme"`
	OrganiserWalkthroughDone bool   `json:"organiser_walkthrough_done"`
	JudgeWalkthroughDone     bool   `json:"judge_walkthrough_done"`
	// EmailUpdates: "Email me when something waits on me" (2026-11-02). On
	// by default; nothing is ever emailed to an account without an address.
	EmailUpdates bool `json:"email_updates"`
}

func GetHackPreferences(ctx context.Context, q Querier, userID string) (HackPreferences, error) {
	var p HackPreferences
	err := q.QueryRowContext(ctx, `SELECT theme, organiser_walkthrough_done, judge_walkthrough_done, email_updates FROM hack_account_preferences WHERE user_id = $1`, userID).Scan(&p.Theme, &p.OrganiserWalkthroughDone, &p.JudgeWalkthroughDone, &p.EmailUpdates)
	if err == nil {
		return p, nil
	}
	if err == sql.ErrNoRows {
		return HackPreferences{Theme: "system", EmailUpdates: true}, nil
	}
	return HackPreferences{}, err
}

func SetHackPreferences(ctx context.Context, q Querier, userID string, p HackPreferences) error {
	_, err := q.ExecContext(ctx, `INSERT INTO hack_account_preferences (user_id,theme,organiser_walkthrough_done,judge_walkthrough_done,email_updates) VALUES ($1,$2,$3,$4,$5)
        ON CONFLICT (user_id) DO UPDATE SET theme=EXCLUDED.theme,organiser_walkthrough_done=EXCLUDED.organiser_walkthrough_done,judge_walkthrough_done=EXCLUDED.judge_walkthrough_done,email_updates=EXCLUDED.email_updates`, userID, p.Theme, p.OrganiserWalkthroughDone, p.JudgeWalkthroughDone, p.EmailUpdates)
	return err
}
