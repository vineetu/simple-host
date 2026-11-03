package events

import (
	"testing"
	"time"
)

func TestReminderIn(t *testing.T) {
	cases := map[string]string{"30m": "in less than an hour", "70m": "in about an hour", "5h10m": "in about 5 hours", "23h50m": "in about 24 hours"}
	for in, want := range cases {
		d, _ := time.ParseDuration(in)
		if got := ReminderIn(d); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}
