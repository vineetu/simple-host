package papermetrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	platform "github.com/vsriram/simple-host/internal/papermetrics"
)

// With PAPER_METRICS=off no request carries a measurement, so the recorder's
// stage and phase calls queue nothing and no run is registered.
func TestHackOffRecordsNothing(t *testing.T) {
	d := isolatedDB(t)
	t.Setenv("PAPER_METRICS", "off")
	c := Install(platform.New(d, ""))
	c.platform.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		RequestRecorder{}.RecordHackStage(r.Context(), "ignored", "open")
		RequestRecorder{}.HackPhasesChanged(r.Context())
		RequestRecorder{}.ResolveSite(r.Context(), "account", "site", "member", true)
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/hack/events/ignored", nil))
	if len(c.hack.phases) != 0 || len(c.hack.refresh) != 0 {
		t.Fatalf("off queued %d phases, %d refreshes", len(c.hack.phases), len(c.hack.refresh))
	}
	c.flushHack(context.Background(), time.Now().UTC().Add(2*time.Minute))
	var runs, minutes int
	if err := d.QueryRow(`SELECT (SELECT count(*) FROM paper_run),(SELECT count(*) FROM paper_hack_minute)`).Scan(&runs, &minutes); err != nil || runs != 0 || minutes != 0 {
		t.Fatalf("off wrote %d runs, %d minutes: %v", runs, minutes, err)
	}
}
