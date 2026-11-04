package sites

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/model"
)

func TestCreateRequiresUser(t *testing.T) {
	s := New(nil, nil, nil, 0, Deps{})
	for _, create := range []func(context.Context, CreateInput) (CreateResult, error){s.Create} {
		_, err := create(context.Background(), CreateInput{})
		var siteErr *Error
		if !errors.As(err, &siteErr) || siteErr.Code != "unauthorized" {
			t.Fatalf("create without user: got %v", err)
		}
	}
}

func TestPreviewExpiry(t *testing.T) {
	s := New(nil, nil, map[string]bool{"preview": true}, time.Hour, Deps{})
	if got := s.previewExpiry("other"); got != nil {
		t.Fatalf("permanent account expiry = %v", got)
	}
	before := time.Now().Add(time.Hour)
	got := s.previewExpiry("PREVIEW")
	after := time.Now().Add(time.Hour)
	if got == nil || got.Before(before) || got.After(after) {
		t.Fatalf("preview expiry = %v, want between %v and %v", got, before, after)
	}
}

func TestCreateConflict(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		held, orUpdate, exists bool
		code, message          string
	}{
		{name: "held name takes precedence", held: true, orUpdate: true, code: "recently_deleted", message: "held"},
		{name: "update existing", orUpdate: true, exists: true},
		{name: "duplicate", code: "site_exists"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := createConflict(CreateInput{User: &model.User{ID: "owner"}, Name: "example", OrUpdate: tc.orUpdate}, model.Site{ID: "site"}, "held", tc.held)
			if result.Exists != tc.exists || result.Site.ID != "site" {
				t.Fatalf("result = %+v", result)
			}
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var siteErr *Error
			if !errors.As(err, &siteErr) || siteErr.Code != tc.code || (tc.message != "" && siteErr.Message != tc.message) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
