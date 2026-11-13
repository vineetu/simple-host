package hackevents

import (
	"testing"
	"time"
)

// A date-only end lasts the whole of that day.
func TestEventOverDateOnlyEnd(t *testing.T) {
	jkt, _ := time.LoadLocation("Asia/Jakarta")
	midnight := time.Date(2026, 11, 4, 0, 0, 0, 0, jkt)
	if got, want := EventOver(midnight, "Asia/Jakarta"), time.Date(2026, 11, 5, 0, 0, 0, 0, jkt); !got.Equal(want) {
		t.Fatalf("date-only end over at %v, want %v", got, want)
	}
	timed := time.Date(2026, 11, 4, 17, 30, 0, 0, jkt)
	if got := EventOver(timed, "Asia/Jakarta"); !got.Equal(timed) {
		t.Fatalf("timed end over at %v, want %v", got, timed)
	}
	// Midnight in UTC is 7 am in Jakarta: not a date-only end there.
	utcMidnight := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	if got := EventOver(utcMidnight, "Asia/Jakarta"); !got.Equal(utcMidnight) {
		t.Fatalf("UTC midnight in Jakarta: %v", got)
	}
	if got := EventOver(utcMidnight, ""); !got.Equal(utcMidnight.AddDate(0, 0, 1)) {
		t.Fatalf("no zone: %v", got)
	}
}
