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
		{"own address, no unlock", Visit{Protected: true, Row: locked}, VisitNeedPasscode},
		{"own address, unlocked", Visit{Protected: true, Row: locked, Who: Visitor{Unlocked: true}}, VisitServe},
		{"unlock never lent to another site's file", Visit{Protected: true, Row: locked, Who: Visitor{Unlocked: true, CrossSite: true}}, VisitNeedPasscode},
		{"a key never opens a page", Visit{Protected: true, Row: locked, Who: Visitor{OwnerKey: true, AdminKey: true, Read: true, Preview: true}}, VisitNeedPasscode},
		{"the owner's key opens the data", Visit{Kind: VisitData, Protected: true, Row: locked, Who: Visitor{OwnerKey: true}}, VisitServe},
		{"the admin's key passes the passcode", Visit{Kind: VisitData, Protected: true, Row: locked, Who: Visitor{AdminKey: true}}, VisitServe},
		{"a preview reads the data", Visit{Kind: VisitData, Protected: true, Row: locked, Who: Visitor{Read: true, Preview: true}}, VisitServe},
		{"a preview never writes", Visit{Kind: VisitData, Protected: true, Row: locked, Who: Visitor{Preview: true}}, VisitNeedPasscode},
		{"data on a shared host is not sent elsewhere", Visit{Kind: VisitData, Protected: true, Row: locked, SharedHost: true}, VisitNeedPasscode},
		{"viewers: signed out", Visit{Protected: true, Row: viewers}, VisitNeedSignIn},
		{"viewers: framed", Visit{Protected: true, Row: viewers, Who: Visitor{Session: true, Listed: true, Framed: true}}, VisitFramedOut},
		{"viewers: not listed", Visit{Protected: true, Row: viewers, Who: Visitor{Session: true}}, VisitNotListed},
		{"viewers: list unreadable", Visit{Protected: true, Row: viewers, Who: Visitor{Session: true, ListErr: errors.New("down")}}, VisitServiceError},
		{"viewers: listed", Visit{Protected: true, Row: viewers, Who: Visitor{Session: true, Listed: true}}, VisitServe},
		{"viewers data: cross-origin write", Visit{Kind: VisitData, Protected: true, Row: viewers, Who: Visitor{Session: true, Listed: true}}, VisitNeedSignIn},
		{"viewers data: same origin", Visit{Kind: VisitData, Protected: true, Row: viewers, Who: Visitor{Session: true, Listed: true, SameOrigin: true}}, VisitServe},
		{"viewers data: reading navigation", Visit{Kind: VisitData, Protected: true, Row: viewers, Who: Visitor{Read: true, Session: true, Listed: true}}, VisitServe},
		{"viewers data: the admin's key", Visit{Kind: VisitData, Protected: true, Row: viewers, Who: Visitor{AdminKey: true}}, VisitServe},
		{"viewers storage: an unlisted visitor's request also carrying the owner's key (storage measures the visitor without it)", Visit{Kind: VisitData, Protected: true, Row: viewers, Who: Visitor{Session: true, SameOrigin: true}}, VisitNotListed},
		{"viewers data: the owner's key", Visit{Kind: VisitData, Protected: true, Row: viewers, Who: Visitor{OwnerKey: true}}, VisitServe},
		{"sign-in routes skip named viewers", Visit{Kind: VisitSignIn, Protected: true, Row: viewers}, VisitServe},
	} {
		if got := DecideVisit(tc.v); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}
