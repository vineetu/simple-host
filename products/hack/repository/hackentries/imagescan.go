package hackentries

import (
	"context"

	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// ClearEventScreenshot removes a team's entry screenshot, only while the
// stored bytes still hash to sha, and leaves the entry text and its last
// writer as they are. Reports whether it did.
func ClearEventScreenshot(ctx context.Context, q sqlkit.Querier, teamID, sha string) (bool, error) {
	res, err := q.ExecContext(ctx, `UPDATE event_entries SET screenshot = NULL, screenshot_type = ''
		 WHERE team_id = $1 AND screenshot IS NOT NULL AND encode(sha256(screenshot), 'hex') = $2`, teamID, sha)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
