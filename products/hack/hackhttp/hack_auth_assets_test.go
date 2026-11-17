package hackhttp

import (
	platformassets "github.com/vsriram/simple-host/host/webassets"
	"strings"
	"testing"
)

func TestHackAuthJSHasOnlyCurrentStorage(t *testing.T) {
	raw, err := platformassets.Static.ReadFile("static/auth.js")
	if err != nil {
		t.Fatal(err)
	}
	hack := string(hackAuthJS(raw))
	for _, keep := range []string{"    requireSignIn: function", "    storage: {", "    mount: function", "    email: {"} {
		if !strings.Contains(hack, keep) {
			t.Errorf("Hack auth.js lost %q", keep)
		}
	}
	for _, removed := range []string{"    state: {", "    collection: function", "    data: function", "SH.data(", "SH.collection("} {
		if strings.Contains(hack, removed) {
			t.Errorf("Hack auth.js still offers %q", removed)
		}
	}
	if !strings.Contains(string(raw), "    state: {") || !strings.Contains(string(raw), "    data: function") {
		t.Fatal("Host auth.js lost its legacy API")
	}
	rawStr := string(raw)
	for _, m := range []string{"    state: {\n", "    storage: {\n", "    collection: function (name) {\n", "    mount: function (target) {\n"} {
		if strings.Count(rawStr, m) != 1 {
			t.Errorf("auth.js marker %q appears %d times, want 1", m, strings.Count(rawStr, m))
		}
	}
}
