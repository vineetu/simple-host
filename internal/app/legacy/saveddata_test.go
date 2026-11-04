package legacy

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	model "github.com/vsriram/simple-host/internal/model"
)

func TestReadBucketLookupOrder(t *testing.T) {
	for _, tc := range []struct {
		name, key, want string
		valid           bool
		calls           []string
	}{
		{"valid key", "valid", "user:owner", true, []string{"key"}},
		{"invalid key", "invalid", "site:site|192.0.2.1", false, []string{"key"}},
		{"no key", "", "site:site|192.0.2.1", false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			a := New(nil, time.Time{}, Deps{
				VisitorResolveKey: func(_ context.Context, _ string) (model.User, bool, error) {
					calls = append(calls, "key")
					return model.User{ID: "owner"}, tc.valid, nil
				},
			})
			if got := a.ReadBucket(context.Background(), ReadBucketInput{SiteID: "site", APIKey: tc.key, ClientIP: "192.0.2.1"}); got != tc.want {
				t.Fatalf("bucket = %q, want %q", got, tc.want)
			}
			if !reflect.DeepEqual(calls, tc.calls) {
				t.Fatalf("calls = %v, want %v", calls, tc.calls)
			}
		})
	}
}

func TestReadBucketKeyLookupErrorFallsBackToSite(t *testing.T) {
	a := New(nil, time.Time{}, Deps{
		VisitorResolveKey: func(context.Context, string) (model.User, bool, error) {
			return model.User{ID: "owner"}, true, errors.New("lookup failed")
		},
	})
	got := a.ReadBucket(context.Background(), ReadBucketInput{SiteID: "site", APIKey: "key", ClientIP: "192.0.2.1"})
	if got != "site:site|192.0.2.1" {
		t.Fatalf("bucket = %q, want site fallback", got)
	}
}

func TestConfigureSavedDataLimits(t *testing.T) {
	a := New(nil, time.Time{}, Deps{})
	limits := a.ConfigureSavedData(context.Background(), SavedDataLimitsInput{ReadBurst: 5, ReadPerSec: 2, AppendBurst: 9, AppendPerMin: 30, BoardWritesPerMin: 15})
	if limits.Read != (RateLimit{5, 2}) || limits.Append != (RateLimit{9, 0.5}) || limits.Board != (RateLimit{15, 0.25}) || limits.Thin != (RateLimit{1, 1}) {
		t.Fatalf("unexpected saved-data limits: %+v", limits)
	}
}

func TestSiteHasRoomWithoutUsageLookup(t *testing.T) {
	a := New(nil, time.Time{}, Deps{})
	for _, in := range []SiteRoomInput{
		{SiteID: "site", Growth: 0, MaxBytes: 10},
		{SiteID: "site", Growth: -1, MaxBytes: 10},
		{SiteID: "site", Growth: 1, MaxBytes: 0},
	} {
		ok, err := a.SiteHasRoom(context.Background(), in)
		if err != nil || !ok {
			t.Fatalf("SiteHasRoom(%+v) = %v, %v; want true, nil", in, ok, err)
		}
	}
}
