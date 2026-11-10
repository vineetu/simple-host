package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/mcp"
)

// Opt-in fixture for scripts/e2e-hack-event-live.mjs: a real uploaded recipe,
// two live team sites/entries, tracks, and scored but unpublished results.
func TestServeHackEventLiveBrowser(t *testing.T) {
	file := os.Getenv("HACK_EVENT_LIVE_FIXTURE")
	if file == "" {
		t.Skip("HACK_EVENT_LIVE_FIXTURE unset")
	}
	w := newCardWorld(t)
	a := w.a
	base := "/v1/hack/events/" + w.slug
	wantTS(t, "reopen", a.api(t, "POST", base+"/stage", map[string]string{"stage": "open"}, w.org.key), 200, "")
	wantTS(t, "tracks", a.api(t, "PUT", base+"/tracks", map[string]any{"tracks": []map[string]string{{"slug": "health", "name": "Health"}, {"slug": "climate", "name": "Climate"}}}, w.org.key), 200, "")
	for _, team := range []struct {
		person person
		track  string
	}{{w.alphaP, "health"}, {w.betaP, "climate"}} {
		wantTS(t, "track", a.api(t, "PUT", base+"/team/track", map[string]string{"track": team.track}, team.person.key), 200, "")
	}
	key := a.teamKey(t, w.slug, w.betaP)
	r := a.deployTeam(t, w.betaSlug, key, "Beta site")
	if r.status != 200 && r.status != 201 {
		t.Fatalf("deploy beta: %d %s", r.status, r.body)
	}
	wantTS(t, "gallery", a.api(t, "PATCH", base, map[string]any{"gallery_open": true, "submission_deadline": time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)}, w.org.key), 200, "")
	wantTS(t, "schedule", a.api(t, "PUT", base+"/content", map[string]any{"schedule": []map[string]string{{"title": "Live kickoff", "start_at": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339), "end_at": time.Now().Add(25 * time.Hour).UTC().Format(time.RFC3339)}}}, w.org.key), 200, "")
	recipe, err := mcp.HackPageRecipe("event", w.slug)
	if err != nil {
		t.Fatal(err)
	}
	_, code, ok := strings.Cut(recipe, "```html\n")
	if !ok {
		t.Fatal("recipe lacks index.html")
	}
	code, _, ok = strings.Cut(code, "\n```")
	if !ok {
		t.Fatal("recipe lacks closing fence")
	}
	code = strings.ReplaceAll(code, "https://simple-hack.app", a.srv.URL)
	wantTS(t, "publish recipe", a.api(t, "PUT", base+"/website/files?create=1", map[string]any{"files": map[string]string{"index.html": code}}, w.org.key), 201, "")
	// A second local origin serves the actual event host through the same
	// production host router, without requiring local DNS or certificates.
	event := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		req.Host = w.host
		req.Header.Set("X-Forwarded-Proto", "https")
		a.srv.Config.Handler.ServeHTTP(rw, req)
	}))
	t.Cleanup(event.Close)
	stop := make(chan struct{})
	a.mux.HandleFunc("POST /_fixture/stop", func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(204)
		select {
		case <-stop:
		default:
			close(stop)
		}
	})
	raw, err := json.Marshal(map[string]any{"url": a.srv.URL, "slug": w.slug, "organiser_key": w.org.key, "event_host_url": event.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stop:
	case <-time.After(30 * time.Minute):
		t.Fatal("event live browser timed out")
	}
}
