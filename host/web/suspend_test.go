package web

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// The /admin sign-in form is not offered over plain http except on this
// computer: the page's <plainHTTP> block, run under node.
func TestAdminSignInPlainHTTP(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	page, err := StaticFiles.ReadFile("static/admin.html")
	if err != nil {
		t.Fatal(err)
	}
	src := string(page)
	i, j := strings.Index(src, "// <plainHTTP>"), strings.Index(src, "// </plainHTTP>")
	if i < 0 || j < i {
		t.Fatal("admin.html lacks the <plainHTTP> block")
	}
	cases := []struct {
		proto, host string
		refuse      bool
	}{
		{"http:", "203.0.113.7", true},
		{"http:", "builds.example.com", true},
		{"http:", "localhost", false},
		{"http:", "app.localhost", false},
		{"http:", "127.0.0.1", false},
		{"http:", "[::1]", false},
		{"https:", "203.0.113.7", false},
		{"https:", "builds.example.com", false},
	}
	var in [][2]string
	for _, c := range cases {
		in = append(in, [2]string{c.proto, c.host})
	}
	b, _ := json.Marshal(in)
	prog := src[i:j] + "\nprocess.stdout.write(JSON.stringify(" + string(b) + ".map(function(c){return keyOverPlainHTTP(c[0],c[1]);})));"
	out, err := exec.Command(node, "-e", prog).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var got []bool
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	for k, c := range cases {
		if got[k] != c.refuse {
			t.Errorf("%s//%s: refuse=%v, want %v", c.proto, c.host, got[k], c.refuse)
		}
	}
	if !strings.Contains(src, "if(keyOverPlainHTTP(location.protocol, location.hostname)){") {
		t.Error("renderSignIn does not check keyOverPlainHTTP before the form")
	}
}
