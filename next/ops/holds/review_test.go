package holds_test

import (
	"testing"

	"simplehost/core/testkit"
	"simplehost/ops/holds"
)

// Review A10: a report's address is kept only when it is http(s), so the
// admin page never shows a script link.
func TestReportLinkIsHTTP(t *testing.T) {
	k := testkit.Start(t, holds.Mount)
	report := func(u string) int {
		return testkit.Call(t, k.Browser(), "POST", "https://simple-host.app/v1/reports", map[string]string{"url": u, "reason": "Phishing."}).Status
	}
	if s := report("javascript://x/%0aalert(document.domain)"); s != 400 {
		t.Fatalf("javascript: address: %d", s)
	}
	if s := report("https://blog--nina.simple-host.site/win"); s != 200 {
		t.Fatalf("https address: %d", s)
	}
}
