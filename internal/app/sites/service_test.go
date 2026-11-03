package sites

import (
	"testing"
	"time"
)

func TestPreviewExpiry(t *testing.T) {
	s := New(nil, nil, nil, map[string]bool{"preview": true}, time.Hour)
	if got := s.PreviewExpiry("other"); got != nil {
		t.Fatalf("permanent account expiry = %v", got)
	}
	before := time.Now().Add(time.Hour)
	got := s.PreviewExpiry("PREVIEW")
	after := time.Now().Add(time.Hour)
	if got == nil || got.Before(before) || got.After(after) {
		t.Fatalf("preview expiry = %v, want between %v and %v", got, before, after)
	}
}
