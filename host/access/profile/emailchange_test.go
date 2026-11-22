package profile

import (
	"testing"
)

func TestMaskEmail(t *testing.T) {
	if got := maskEmail("new.person@example.com"); got != "n***@example.com" {
		t.Errorf("maskEmail = %q", got)
	}
}
