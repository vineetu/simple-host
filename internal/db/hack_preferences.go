package db

import (
	"context"
	"database/sql"
)

type HackPreferences struct {
	Theme                    string `json:"theme"`
	OrganiserWalkthroughDone bool   `json:"organiser_walkthrough_done"`
	JudgeWalkthroughDone     bool   `json:"judge_walkthrough_done"`
}

func GetHackPreferences(ctx context.Context, q Querier, userID string) (HackPreferences, error) {
	var p HackPreferences
	err := q.QueryRowContext(ctx, `SELECT theme, organiser_walkthrough_done, judge_walkthrough_done FROM hack_account_preferences WHERE user_id = $1`, userID).Scan(&p.Theme, &p.OrganiserWalkthroughDone, &p.JudgeWalkthroughDone)
	if err == nil {
		return p, nil
	}
	if err == sql.ErrNoRows {
		return HackPreferences{Theme: "system"}, nil
	}
	return HackPreferences{}, err
}

func SetHackPreferences(ctx context.Context, q Querier, userID string, p HackPreferences) error {
	_, err := q.ExecContext(ctx, `INSERT INTO hack_account_preferences (user_id,theme,organiser_walkthrough_done,judge_walkthrough_done) VALUES ($1,$2,$3,$4)
        ON CONFLICT (user_id) DO UPDATE SET theme=EXCLUDED.theme,organiser_walkthrough_done=EXCLUDED.organiser_walkthrough_done,judge_walkthrough_done=EXCLUDED.judge_walkthrough_done`, userID, p.Theme, p.OrganiserWalkthroughDone, p.JudgeWalkthroughDone)
	return err
}
