package hackevents

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

type HackPreferences = model.HackPreferences

func GetHackPreferences(ctx context.Context, q sqlkit.Querier, userID string) (HackPreferences, error) {
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

func SetHackPreferences(ctx context.Context, q sqlkit.Querier, userID string, p HackPreferences) error {
	_, err := q.ExecContext(ctx, `INSERT INTO hack_account_preferences (user_id,theme,organiser_walkthrough_done,judge_walkthrough_done,email_updates) VALUES ($1,$2,$3,$4,$5)
        ON CONFLICT (user_id) DO UPDATE SET theme=EXCLUDED.theme,organiser_walkthrough_done=EXCLUDED.organiser_walkthrough_done,judge_walkthrough_done=EXCLUDED.judge_walkthrough_done,email_updates=EXCLUDED.email_updates`, userID, p.Theme, p.OrganiserWalkthroughDone, p.JudgeWalkthroughDone, p.EmailUpdates)
	return err
}
