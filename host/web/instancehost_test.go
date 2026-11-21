package web

import (
	"bytes"
	"testing"
)

func TestHostRewriterLongestHostWins(t *testing.T) {
	// The ordering trap: "sites.simple-host.app" contains "simple-host.app".
	// Replace the short one first and the content host becomes
	// "sites.hack.example.com" only by luck, or "sites." + something wrong.
	rw := newHostRewriter("hack.example.com", "sites.hack.example.com", "cname.hack.example.com")
	if rw == nil {
		t.Fatal("expected a rewriter for a non-canonical instance")
	}
	for _, tc := range []struct{ in, want string }{
		{"https://sites.simple-host.app/vineetu/demo/", "https://sites.hack.example.com/vineetu/demo/"},
		{"https://simple-host.app/v1/sites", "https://hack.example.com/v1/sites"},
		{"CNAME @ -> cname.simple-host.app", "CNAME @ -> cname.hack.example.com"},
		{"apex simple-host.app and content sites.simple-host.app", "apex hack.example.com and content sites.hack.example.com"},
		{"nothing to change here", "nothing to change here"},
	} {
		if got := string(rw.Apply([]byte(tc.in))); got != tc.want {
			t.Errorf("apply(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

func TestCopyRewrittenLeavesBinaryAlone(t *testing.T) {
	// A future binary asset in the skills tree must survive the substitution.
	// Corruption inside a downloaded zip is very hard to trace back here.
	SetInstanceHosts("hack.example.com", "sites.hack.example.com", "cname.hack.example.com")
	defer SetInstanceHosts("simple-host.app", "", "")

	binary := []byte{0x89, 'P', 'N', 'G', 0x00, 0x1a, 0xff, 0xfe}
	var out bytes.Buffer
	if err := copyRewritten(&out, bytes.NewReader(binary), nil, false); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), binary) {
		t.Errorf("binary was altered: % x", out.Bytes())
	}

	// Text still gets Rewritten.
	out.Reset()
	if err := copyRewritten(&out, bytes.NewReader([]byte("go to simple-host.app")), nil, false); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "go to hack.example.com" {
		t.Errorf("text not rewritten: %q", got)
	}
}
