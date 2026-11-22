package routegate

import (
	"os"
	"path/filepath"
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
			if !ownerOnly && !strings.Contains(line, "passcodeGate(") && !strings.Contains(line, ".signInGate(") && !(strings.Contains(m[2], "/storage/") && (strings.Contains(lower, ".storage") || strings.Contains(lower, "storageroutefor(caller") || strings.Contains(lower, ".liststorageresources") || strings.Contains(lower, ".getstorageusage") || strings.Contains(lower, ".putstorageresource") || strings.Contains(lower, ".deletestorageresource") || strings.Contains(lower, ".createstoragefilelink"))) {
				t.Errorf("%s: %s %s is neither owner-only nor behind the passcode gate", f, m[1], m[2])
			}
		}
	}
	if n < 70 {
		t.Fatalf("found only %d routes; the pattern no longer matches Register", n)
	}
}

func routeGoFiles() []string {
	var files []string
	// Access and Sites mount their routes from each feature package (host/sites/*/routes.go).
	for _, dir := range []string{"../../host/web", "../../host/access/accesshttp", "../../host/access/gate", "../../host/access/editors", "../../host/access/teams", "../../host/access/keys", "../../host/access/signin", "../../host/access/profile", "../../host/access/accountdata", "../../host/access/operator", "../../host/access/suspension", "../../host/access/connector", "../../host/data", "../../host/data/files", "../../host/data/kv", "../../host/data/notifications", "../../host/data/push", "../../host/data/resources", "../../host/data/rows", "../../host/data/sqlconsole", "../../host/data/usage", "../../host/data/visitors", "../../host/sites/*", "../../host/handler"} {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		files = append(files, matches...)
	}
	return files
}
