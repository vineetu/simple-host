package email

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

func TestHackMailJournalOmitsMessageDetails(t *testing.T) {
	var output bytes.Buffer
	old := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(old) })
	var status atomic.Int32
	status.Store(http.StatusOK)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected request")
		}
		w.WriteHeader(int(status.Load()))
		if status.Load() == http.StatusOK {
			_, _ = w.Write([]byte(`{"id":"mail-123"}`))
		} else {
			_, _ = w.Write([]byte(`{"error":"private@example.com 654321"}`))
		}
	}))
	defer server.Close()
	s := NewResendSender("test-key", "hack@example.com")
	s.SetProductName("Simple Hack")
	s.endpoint = server.URL
	if err := s.SendSignInCode("private@example.com", "654321", "https://private.example/code"); err != nil {
		t.Fatal(err)
	}
	status.Store(http.StatusForbidden)
	if err := s.SendNotice("private@example.com", "private subject", "private body"); err == nil {
		t.Fatal("expected provider failure")
	}
	if got := output.String(); !strings.Contains(got, "hack_mail_send outcome=accepted id=mail-123") ||
		!strings.Contains(got, "hack_mail_send outcome=failed reason=http_403") {
		t.Fatalf("missing aggregate records: %s", got)
	}
	for _, secret := range []string{"private@example.com", "654321", "private subject", "private body", "test-key", "private.example"} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("journal leaked message detail %q", secret)
		}
	}
	output.Reset()
	host := NewResendSender("", "host@example.com")
	_ = host.SendSignInCode("private@example.com", "654321", "")
	if output.Len() != 0 {
		t.Fatalf("Host sender entered Hack report: %s", output.String())
	}
}
