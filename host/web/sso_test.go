package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vsriram/simple-host/host/httpx"
)

func TestSSOSkillReleaseNotifiesPreviousVersion(t *testing.T) {
	version, err := PluginVersion()
	if err != nil {
		t.Fatal(err)
	}
	handler := NoticeMiddleware(version)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { httpx.WriteJSON(w, 200, map[string]string{"ok": "yes"}) }))
	r := httptest.NewRequest("GET", "https://simple-host.app/v1/me", nil)
	r.Header.Set("X-Skill-Version", "0.27.84")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Header().Get("X-Skill-Notice") == "" {
		t.Fatal("pre-SSO skill must receive an upgrade notice")
	}
}
