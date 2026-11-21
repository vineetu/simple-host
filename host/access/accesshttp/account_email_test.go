package accesshttp

import (
	"strings"
	"sync"
	"testing"
)

// mailbox captures sign-in codes and notices.
type mailbox struct {
	mu      sync.Mutex
	codes   map[string]string // address -> last sign-in code
	notices []string          // "to|subject|text"
}

func (m *mailbox) SendSignInCode(to, code, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.codes == nil {
		m.codes = map[string]string{}
	}
	m.codes[to] = code
	return nil
}

func (m *mailbox) SendNotice(to, subject, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notices = append(m.notices, to+"|"+subject+"|"+text)
	return nil
}

const (
	uaMacChrome = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"
	uaIPhone    = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1"
)

func TestSummarizeUserAgent(t *testing.T) {
	cases := map[string]string{
		uaMacChrome:  "Chrome on macOS",
		uaIPhone:     "Safari on iPhone",
		"curl/8.5.0": "curl",
		"":           "an unknown browser or app",
	}
	for ua, want := range cases {
		if got := summarizeUserAgent(ua); got != want {
			t.Errorf("summarizeUserAgent(%q) = %q, want %q", ua, got, want)
		}
	}
	for in, want := range map[string]string{
		"Test Chat":                              "Test Chat",
		"verify at simple-host-support.com now":  "verify at now",
		"see https://evil.example/x or www.x.io": "see or",
		strings.Repeat("a", 60):                  strings.Repeat("a", 40),
	} {
		if got := reportedName(in); got != want {
			t.Errorf("reportedName(%q) = %q, want %q", in, got, want)
		}
	}
}
