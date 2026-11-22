package auth

import (
	"strings"
	"testing"
)

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
		if got := SummarizeUserAgent(ua); got != want {
			t.Errorf("SummarizeUserAgent(%q) = %q, want %q", ua, got, want)
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
