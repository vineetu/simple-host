package handler

import (
	"bytes"
	"testing"
)

func TestHostRewriterIsNilOnCanonicalInstance(t *testing.T) {
	// simple-host.app rewrites to itself, so production must pay nothing and,
	// more importantly, must not risk a substitution bug on its own assets.
	if rw := newHostRewriter("simple-host.app", "sites.simple-host.app", "cname.simple-host.app"); rw != nil {
		t.Fatal("expected nil rewriter on the canonical instance")
	}
	if rw := newHostRewriter("", "", ""); rw != nil {
		t.Fatal("expected nil rewriter when no domain is configured")
	}
}

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
		if got := string(rw.apply([]byte(tc.in))); got != tc.want {
			t.Errorf("apply(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

func TestHostRewriterDerivesMissingHosts(t *testing.T) {
	// An operator who sets only SITE_DOMAIN still gets coherent content and
	// cname hosts, matching the defaults the config package applies.
	rw := newHostRewriter("hack.example.com", "", "")
	got := string(rw.apply([]byte("sites.simple-host.app cname.simple-host.app")))
	if want := "sites.hack.example.com cname.hack.example.com"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNilRewriterLeavesBytesAlone(t *testing.T) {
	var rw *hostRewriter
	in := []byte("https://simple-host.app")
	if got := string(rw.apply(in)); got != string(in) {
		t.Errorf("nil rewriter changed bytes: %q", got)
	}
}

func TestCopyRewrittenLeavesBinaryAlone(t *testing.T) {
	// A future binary asset in the skills tree must survive the substitution.
	// Corruption inside a downloaded zip is very hard to trace back here.
	SetInstanceHosts("hack.example.com", "sites.hack.example.com", "cname.hack.example.com")
	defer SetInstanceHosts("simple-host.app", "", "")

	binary := []byte{0x89, 'P', 'N', 'G', 0x00, 0x1a, 0xff, 0xfe}
	var out bytes.Buffer
	if err := copyRewritten(&out, bytes.NewReader(binary), false); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), binary) {
		t.Errorf("binary was altered: % x", out.Bytes())
	}

	// Text still gets rewritten.
	out.Reset()
	if err := copyRewritten(&out, bytes.NewReader([]byte("go to simple-host.app")), false); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "go to hack.example.com" {
		t.Errorf("text not rewritten: %q", got)
	}
}

func TestControlPlaneSkillIsNotRewritten(t *testing.T) {
	// The hackathon skill calls /v1/events on the PUBLIC instance, which is the
	// only place that endpoint exists. Rewriting it to an event's own host would
	// send an agent to a box that answers 404, and teardown would then leave
	// live records in our zone.
	for _, p := range []string{"run-hackathon/SKILL.md", "skills/run-hackathon/references/dns.md"} {
		if !controlPlaneSkill(p) {
			t.Errorf("%s should be exempt from rewriting", p)
		}
	}
	for _, p := range []string{"website-deploy/SKILL.md", "connect-domain/references/registrars.md"} {
		if controlPlaneSkill(p) {
			t.Errorf("%s should be rewritten", p)
		}
	}

	SetInstanceHosts("hack.example.com", "", "")
	defer SetInstanceHosts("simple-host.app", "", "")
	var out bytes.Buffer
	in := []byte("POST https://simple-host.app/v1/events")
	if err := copyRewritten(&out, bytes.NewReader(in), true); err != nil {
		t.Fatal(err)
	}
	if out.String() != string(in) {
		t.Errorf("control-plane URL was rewritten to %q", out.String())
	}
}
