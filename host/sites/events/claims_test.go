package events

import "testing"

// An event name claim takes only what Simple Hack's own check takes.
func TestNormalizeEventNameSharesProductShape(t *testing.T) {
	for _, bad := range []string{"a--b", "ab", "abcdefghij-abcdefghij-abcdefghij-abcdefg"} {
		if _, err := NormalizeEventName(bad); err == nil {
			t.Fatal(bad)
		}
	}
	if n, err := NormalizeEventName(" Meetup "); err != nil || n != "meetup" {
		t.Fatal(n, err)
	}
}
