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
}
