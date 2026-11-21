package accesshttp

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/web"
)

// Small-box trial B7/B14: an install's llms.txt starts with what holds there,
// and its messages name its own contact, not the hosted support mailbox.
func TestInstanceNoteAndContact(t *testing.T) {
	prevHosts, prevNote, prevContact := web.InstanceHosts, web.InstanceNote, auth.SupportContact
	t.Cleanup(func() {
		web.InstanceHosts, web.InstanceNote = prevHosts, prevNote
		auth.SetSupportContact(prevContact, true)
	})
	web.SetInstanceHosts("ev.test", "sites.ev.test", "")
	auth.SetSupportContact("whoever runs this server", false)
	web.SetInstanceNote(web.InstanceFacts{SiteDomain: "ev.test", ContentHost: "sites.ev.test", SharedOrigin: true, Contact: auth.SupportContact})
	rec := httptest.NewRecorder()
	testAssetPipeline(edition.Default(config.DefaultLimits()), "").ServeRewrittenAsset("llms.txt", web.InstanceHosts, time.Now()).ServeHTTP(rec, httptest.NewRequest("GET", "/llms.txt", nil))
	body := rec.Body.String()
	for _, want := range []string{"THIS SERVER (ev.test)", "shares that one browser origin", "Never call SH.requireSignIn()",
		"preview_unavailable", "sends no email", "ask whoever runs this server",
		"(one browser origin, shared by every site on this server)"} {
		if !strings.Contains(body, want) {
			t.Errorf("llms.txt lacks %q", want)
		}
	}
	if !strings.HasPrefix(body, "THIS SERVER") {
		t.Errorf("the note is not first: %.80s", body)
	}
	if got := exportReadme(); strings.Contains(got, "support@simple-host.app") || !strings.Contains(got, "whoever runs this server") {
		t.Errorf("export README names the hosted mailbox")
	}
	if msg := auth.InvalidKeyMessage(); strings.Contains(msg, "/v1/auth") {
		t.Errorf("invalid key message sends to email sign-in: %s", msg)
	}
	// On simple-host.app itself: no note.
	web.SetInstanceHosts("simple-host.app", "", "")
	web.SetInstanceNote(web.InstanceFacts{SiteDomain: "simple-host.app"})
	if web.InstanceNote != "" {
		t.Errorf("a note on the hosted service")
	}
}
