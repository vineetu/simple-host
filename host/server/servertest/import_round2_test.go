package servertest

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/config"
)

type bodyDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *bodyDeadlineRecorder) SetReadDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func checkBodyDeadline(t *testing.T, w *bodyDeadlineRecorder, start time.Time, want time.Duration) {
	t.Helper()
	if len(w.deadlines) != 2 || !w.deadlines[1].IsZero() {
		t.Fatalf("deadline was not set and cleared: %v", w.deadlines)
	}
	if got := w.deadlines[0]; got.Before(start.Add(want)) || got.After(time.Now().Add(want)) {
		t.Fatalf("deadline %v, want %v after admission", got, want)
	}
}

func TestKeepJSONBodyAllowanceDeadline(t *testing.T) {
	a, user := KeepReviewSite(t)
	user.Handle.String, user.Handle.Valid = "deadline-owner", true
	old := config.DefaultLimits()
	for _, mb := range []int{1, 500} {
		t.Run(fmt.Sprint(mb), func(t *testing.T) {
			l := old
			l.MaxArchiveOverrides = fmt.Sprintf("deadline-owner:%d", mb)
			a.Sites.Limits = l
			bodyLimit := (int64(mb)<<20)*4/3 + (1 << 20)
			want := max(15*time.Minute, time.Duration(bodyLimit)*time.Second/250000+10*time.Minute)
			w := &bodyDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
			start := time.Now()
			_, _, limit, override, err := a.Sites.DecodeJSONFiles(w, KeepReviewRequest(user, strings.NewReader(`{}`)))
			if err != nil || !override || limit != int64(mb)<<20 {
				t.Fatal(limit, override, err, w.Body.String())
			}
			checkBodyDeadline(t, w, start, want)
		})
	}
}
