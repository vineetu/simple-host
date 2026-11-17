package legacy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestListMineResolvesBeforeLimiting(t *testing.T) {
	for _, tc := range []struct {
		name       string
		resolveErr error
		wantCode   string
		wantLimit  bool
	}{
		{name: "missing site", resolveErr: errors.New("lookup failed"), wantCode: "site_not_found"},
		{name: "limited", wantCode: "rate_limited", wantLimit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limited := false
			a := New(nil, time.Time{}, Deps{
				MineResolveSite: func(name string) (string, error) {
					if name != "shop" {
						t.Fatalf("site = %q", name)
					}
					return "site-id", tc.resolveErr
				},
				MineReadLimit: func(id string) bool {
					limited = true
					if id != "site-id" {
						t.Fatalf("site ID = %q", id)
					}
					return false
				},
			})
			_, err := a.ListMine(context.Background(), ListMineInput{SiteName: "shop"})
			var opErr *Error
			if !errors.As(err, &opErr) || opErr.Code != tc.wantCode {
				t.Fatalf("error = %v, want %s", err, tc.wantCode)
			}
			if limited != tc.wantLimit {
				t.Fatalf("limiter called = %t, want %t", limited, tc.wantLimit)
			}
		})
	}
}

func TestGetDataKindPreludeOrder(t *testing.T) {
	var calls []string
	a := New(nil, time.Time{}, Deps{
		KindOwnerSite:     func(string) (string, bool) { calls = append(calls, "owner"); return "", false },
		KindOriginAllowed: func(string) bool { calls = append(calls, "origin"); return true },
		KindResolveSite:   func(string) (string, error) { calls = append(calls, "resolve"); return "site", nil },
		KindReadLimit:     func(string) bool { calls = append(calls, "limit"); return false },
	})
	_, err := a.GetDataKind(context.Background(), GetDataKindInput{SiteName: "shop", Name: "menu", ValidName: true})
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != "rate_limited" {
		t.Fatalf("error = %v", err)
	}
	if got := strings.Join(calls, ","); got != "owner,origin,resolve,limit" {
		t.Fatalf("calls = %s", got)
	}
}
