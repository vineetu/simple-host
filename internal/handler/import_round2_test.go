package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/edition"
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

func TestImportReceivingBodyAllowanceDeadline(t *testing.T) {
	old := config.DefaultLimits()
	for _, tc := range []struct {
		mb   int
		want time.Duration
	}{
		{1, 15 * time.Minute},
		{500, 44*time.Minute + 57*time.Second + 152*time.Millisecond},
		{1024, 81*time.Minute + 34*time.Second + 967296*time.Microsecond},
		{10240, 725*time.Minute + 49*time.Second + 672960*time.Microsecond},
	} {
		t.Run(fmt.Sprint(tc.mb), func(t *testing.T) {
			l := old
			l.ImportMaxUploadMB = tc.mb
			h, root, caller := newImportTest(t, edition.ForKindWithLimits(edition.Host, l))
			token, _ := mintImportTest(t, h, caller)
			// Malformed multipart stops before conversion, after deadline setup.
			r := httptest.NewRequest("POST", "/v1/imports/"+token, strings.NewReader("bad body"))
			w := &bodyDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
			start := time.Now()
			root.ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest {
				t.Fatal(w.Code, w.Body.String())
			}
			checkBodyDeadline(t, w, start, tc.want)
			if len(h.imports.Slots) != 0 {
				t.Fatal("receiving admission leaked")
			}
		})
	}
}

func TestKeepJSONBodyAllowanceDeadline(t *testing.T) {
	a, user := keepReviewSite(t)
	user.Handle.String, user.Handle.Valid = "deadline-owner", true
	old := config.DefaultLimits()
	for _, mb := range []int{1, 500} {
		t.Run(fmt.Sprint(mb), func(t *testing.T) {
			l := old
			l.MaxArchiveOverrides = fmt.Sprintf("deadline-owner:%d", mb)
			a.sites.limitSet = newLimitSet(l)
			bodyLimit := (int64(mb)<<20)*4/3 + (1 << 20)
			want := max(15*time.Minute, time.Duration(bodyLimit)*time.Second/250000+10*time.Minute)
			w := &bodyDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
			start := time.Now()
			_, _, limit, override, err := a.sites.decodeJSONFiles(w, keepReviewRequest(user, strings.NewReader(`{}`)))
			if err != nil || !override || limit != int64(mb)<<20 {
				t.Fatal(limit, override, err, w.Body.String())
			}
			checkBodyDeadline(t, w, start, want)
		})
	}
}
