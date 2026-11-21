package web

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every Go route to a site's page-facing data is either the owner's
// (authMiddleware) or behind the passcode gate; a new route that is neither
// fails here. OPTIONS preflights carry nothing.
func TestPasscodeRouteTable(t *testing.T) {
	files := routeGoFiles()
	route := regexp.MustCompile(`mux\.Handle(?:Func)?\("([A-Z]+) (/v1/(?:u/\{handle\}/)?sites/\{sitename\}/[^"]*)"`)
	n := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			m := route.FindStringSubmatch(line)
			if m == nil || m[1] == "OPTIONS" {
				continue
			}
			n++
			lower := strings.ToLower(line)
			ownerOnly := strings.Contains(line, "authMiddleware(") || strings.Contains(line, "wrap(") && strings.Contains(string(b), "return noticeMiddleware(authMiddleware(f))")
			if !ownerOnly && !strings.Contains(line, "passcodeGate(") && !strings.Contains(line, ".signInGate(") && !(strings.Contains(m[2], "/storage/") && (strings.Contains(lower, ".storage") || strings.Contains(lower, ".liststorageresources") || strings.Contains(lower, ".getstorageusage") || strings.Contains(lower, ".putstorageresource") || strings.Contains(lower, ".deletestorageresource") || strings.Contains(lower, ".createstoragefilelink"))) {
				t.Errorf("%s: %s %s is neither owner-only nor behind the passcode gate", f, m[1], m[2])
			}
		}
	}
	if n < 70 {
		t.Fatalf("found only %d routes; the pattern no longer matches Register", n)
	}
}
