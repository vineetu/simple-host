package messages

import (
	"database/sql"
	"testing"
	"time"

	"github.com/vsriram/simple-host/products/hack/app/events"
)

func TestRateLimit(t *testing.T) {
	old := sql.NullTime{Valid: true, Time: time.Now().Add(-59*time.Minute - 59*time.Second)}
	for _, tc := range []struct {
		used  int
		old   sql.NullTime
		retry int
	}{
		{50, old, 0},               // at the limit is allowed
		{51, sql.NullTime{}, 3601}, // no oldest row: wait the whole window
		{51, old, 2},               // floor of one second, plus one
	} {
		err := rateLimit(tc.used, 50, tc.old, time.Hour)
		got := 0
		if rl, ok := err.(*events.RateLimitError); ok {
			got = rl.RetryAfter
		}
		if got != tc.retry {
			t.Errorf("used %d: retry %d want %d", tc.used, got, tc.retry)
		}
	}
}
