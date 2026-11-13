package teams

import (
	"database/sql"
	"testing"

	"github.com/vsriram/simple-host/internal/model"
)

func TestRequestEventCert(t *testing.T) {
	for _, tc := range []struct {
		stage string
		down  bool
		want  bool
	}{
		{"draft", false, false},
		{"open", false, true},
		{"building", false, true},
		{"closed", false, true},
		{"ended", false, false},
		{"open", true, false},
	} {
		t.Run(tc.stage+map[bool]string{true: "/down", false: "/up"}[tc.down], func(t *testing.T) {
			ev := model.Event{Slug: "event", Stage: tc.stage, TakenDownAt: sql.NullTime{Valid: tc.down}}
			var got []string
			new(Service).RequestEventCert(ev, func(slug string) { got = append(got, slug) })
			if (len(got) == 1 && got[0] == ev.Slug) != tc.want || len(got) > 1 {
				t.Fatalf("requests = %v, want request = %t", got, tc.want)
			}
			new(Service).RequestEventCert(ev, nil)
		})
	}
}
