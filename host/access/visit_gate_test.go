package access

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/vsriram/simple-host/host/model"
)

func TestDecideVisit(t *testing.T) {
	locked := model.SitePasscodeRow{Enc: []byte("x")}
	viewers := model.SitePasscodeRow{NamedViewers: true}
	for _, tc := range []struct {
		name string
		v    Visit
		want VisitVerdict
	}{
		{"open", Visit{}, VisitServe},
		{"taken down wins over all", Visit{Suspended: true, Offline: true, Protected: true, Row: locked}, VisitTakedown},
		{"offline wins over protection", Visit{Offline: true, Protected: true, Row: locked}, VisitOffline},
		{"gone while bound", Visit{Protected: true, RowErr: sql.ErrNoRows, Bound: true}, VisitNotFound},
		{"gone by name", Visit{Protected: true, RowErr: sql.ErrNoRows}, VisitServe},
		{"database down fails closed", Visit{Protected: true, RowErr: errors.New("down")}, VisitServiceError},
		{"stale marker", Visit{Protected: true}, VisitServe},
		{"shared host", Visit{Protected: true, Row: locked, SharedHost: true}, VisitElsewhere},
		{"named viewers on shared host", Visit{Protected: true, Row: viewers, SharedHost: true}, VisitElsewhere},
		{"own address", Visit{Protected: true, Row: locked}, VisitCheckVisitor},
	} {
		if got := DecideVisit(tc.v); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
	if !PasscodeLetsVisitorIn(viewers, false, true) || PasscodeLetsVisitorIn(locked, true, true) || !PasscodeLetsVisitorIn(locked, true, false) || PasscodeLetsVisitorIn(locked, false, false) {
		t.Error("PasscodeLetsVisitorIn")
	}
}
