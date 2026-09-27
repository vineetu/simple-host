package email

import (
	"strings"
	"testing"
)

// The sign-in email says how long its code works, from SIGNIN_CODE_TTL_MINUTES
// (main hands the words over); unset, it keeps saying 15 minutes.
func TestSignInCodeLifetimeWording(t *testing.T) {
	s := NewResendSender("k", "from@example.com")
	_, text, html := s.signInCodeMessage("123456", "")
	for _, body := range []string{text, html} {
		if !strings.Contains(body, "This code expires in 15 minutes.") {
			t.Fatalf("default wording missing: %q", body)
		}
	}
	s.SetCodeLifetime("25 minutes")
	_, text, html = s.signInCodeMessage("123456", "https://example.com/l")
	for _, body := range []string{text, html} {
		if !strings.Contains(body, "This code expires in 25 minutes.") || strings.Contains(body, "15 minutes") {
			t.Fatalf("configured wording missing: %q", body)
		}
	}
}
